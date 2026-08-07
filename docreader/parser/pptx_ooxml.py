"""Minimal, dependency-free OOXML reader for PPTX semantic structure."""

from __future__ import annotations

import posixpath
import zipfile
from dataclasses import dataclass
from io import BytesIO
from xml.etree import ElementTree


_PRESENTATION_NS = "http://schemas.openxmlformats.org/presentationml/2006/main"
_DRAWING_NS = "http://schemas.openxmlformats.org/drawingml/2006/main"
_OFFICE_REL_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
_PACKAGE_REL_NS = "http://schemas.openxmlformats.org/package/2006/relationships"
_NS = {"p": _PRESENTATION_NS, "a": _DRAWING_NS, "r": _OFFICE_REL_NS}
_REL_NS = {"rel": _PACKAGE_REL_NS}


@dataclass(frozen=True)
class PPTXParagraph:
    text: str
    is_list: bool


@dataclass(frozen=True)
class PPTXShape:
    kind: str
    top: int
    left: int
    paragraphs: tuple[PPTXParagraph, ...] = ()
    table: tuple[tuple[str, ...], ...] = ()


@dataclass(frozen=True)
class PPTXSlide:
    number: int
    shapes: tuple[PPTXShape, ...]


def read_pptx_slides(content: bytes) -> tuple[PPTXSlide, ...]:
    with zipfile.ZipFile(BytesIO(content)) as archive:
        presentation = _parse_xml(archive, "ppt/presentation.xml")
        relationships = _presentation_relationships(archive)
        slides: list[PPTXSlide] = []
        for number, slide_id in enumerate(
            presentation.findall("p:sldIdLst/p:sldId", _NS), start=1
        ):
            relation_id = slide_id.get(f"{{{_OFFICE_REL_NS}}}id", "")
            target = relationships.get(relation_id)
            if not target:
                raise ValueError(f"missing slide relationship {relation_id!r}")
            slide = _parse_xml(archive, target)
            slides.append(PPTXSlide(number=number, shapes=_slide_shapes(slide)))
        return tuple(slides)


def _parse_xml(archive: zipfile.ZipFile, path: str) -> ElementTree.Element:
    return ElementTree.fromstring(archive.read(path))


def _presentation_relationships(archive: zipfile.ZipFile) -> dict[str, str]:
    root = _parse_xml(archive, "ppt/_rels/presentation.xml.rels")
    relationships: dict[str, str] = {}
    for relation in root.findall("rel:Relationship", _REL_NS):
        if relation.get("TargetMode") == "External":
            continue
        relation_id = relation.get("Id", "")
        target = relation.get("Target", "")
        if target.startswith("/"):
            resolved = posixpath.normpath(target.lstrip("/"))
        else:
            resolved = posixpath.normpath(posixpath.join("ppt", target))
        if resolved.startswith("ppt/slides/"):
            relationships[relation_id] = resolved
    return relationships


def _slide_shapes(slide: ElementTree.Element) -> tuple[PPTXShape, ...]:
    tree = slide.find("p:cSld/p:spTree", _NS)
    if tree is None:
        return ()
    shapes: list[PPTXShape] = []
    for element in tree:
        tag = element.tag.rsplit("}", 1)[-1]
        shape = _text_shape(element) if tag == "sp" else None
        if tag == "graphicFrame":
            shape = _table_shape(element)
        if shape is not None:
            shapes.append(shape)
    return tuple(sorted(shapes, key=lambda item: (item.top, item.left)))


def _text_shape(element: ElementTree.Element) -> PPTXShape | None:
    paragraphs = tuple(
        paragraph
        for raw in element.findall("p:txBody/a:p", _NS)
        if (paragraph := _shape_paragraph(raw)).text
    )
    if not paragraphs:
        return None
    placeholder = element.find("p:nvSpPr/p:nvPr/p:ph", _NS)
    placeholder_type = placeholder.get("type", "") if placeholder is not None else ""
    kind = "title" if placeholder_type in {"title", "ctrTitle"} else "text"
    top, left = _shape_position(element)
    if kind == "title":
        top = min(top, -1)
    return PPTXShape(kind=kind, top=top, left=left, paragraphs=paragraphs)


def _shape_paragraph(element: ElementTree.Element) -> PPTXParagraph:
    text = "".join(node.text or "" for node in element.findall(".//a:t", _NS))
    properties = element.find("a:pPr", _NS)
    is_list = properties is not None and (
        properties.find("a:buChar", _NS) is not None
        or properties.find("a:buAutoNum", _NS) is not None
    )
    return PPTXParagraph(text=text.strip(), is_list=is_list)


def _table_shape(element: ElementTree.Element) -> PPTXShape | None:
    table = element.find("a:graphic/a:graphicData/a:tbl", _NS)
    if table is None:
        return None
    rows: list[tuple[str, ...]] = []
    for row in table.findall("a:tr", _NS):
        cells = tuple(_table_cell_text(cell) for cell in row.findall("a:tc", _NS))
        if cells:
            rows.append(cells)
    if not rows:
        return None
    top, left = _shape_position(element)
    return PPTXShape(kind="table", top=top, left=left, table=tuple(rows))


def _table_cell_text(cell: ElementTree.Element) -> str:
    paragraphs = [
        "".join(node.text or "" for node in paragraph.findall(".//a:t", _NS))
        for paragraph in cell.findall("a:txBody/a:p", _NS)
    ]
    return "\n".join(value.strip() for value in paragraphs if value.strip())


def _shape_position(element: ElementTree.Element) -> tuple[int, int]:
    offset = element.find("p:xfrm/a:off", _NS)
    if offset is None:
        offset = element.find(".//a:xfrm/a:off", _NS)
    if offset is None:
        return -1, -1
    return int(offset.get("y", "0")), int(offset.get("x", "0"))
