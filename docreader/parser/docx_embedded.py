from __future__ import annotations

import io
import posixpath
import zipfile
from collections.abc import Iterable
from dataclasses import dataclass
from urllib.parse import unquote

from lxml import etree

from docreader.parser.docx_embedded_security import (
    OUTER_LIMITS,
    is_safe_embedded_docx,
    validate_archive,
)

DOCUMENT_XML = "word/document.xml"
DOCUMENT_RELS = "word/_rels/document.xml.rels"

MAX_EMBEDDED_DOCUMENTS = 8
MAX_EMBEDDED_DOCUMENT_BYTES = 20 * 1024 * 1024
MAX_EMBEDDED_TOTAL_BYTES = 40 * 1024 * 1024

W_NS = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
O_NS = "urn:schemas-microsoft-com:office:office"
V_NS = "urn:schemas-microsoft-com:vml"
R_NS = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
PKG_REL_NS = "http://schemas.openxmlformats.org/package/2006/relationships"
OLE_REL_SUFFIX = "/oleObject"

NS = {"w": W_NS, "o": O_NS, "v": V_NS, "r": R_NS}


@dataclass(frozen=True)
class EmbeddedDocument:
    marker: str
    name: str
    file_type: str
    content: bytes


@dataclass(frozen=True)
class DocxPreprocessResult:
    content: bytes
    documents: tuple[EmbeddedDocument, ...] = ()
    warnings: tuple[str, ...] = ()


def preprocess_docx(content: bytes) -> DocxPreprocessResult:
    """Remove OLE preview icons and extract only verified embedded DOCX files."""
    with zipfile.ZipFile(io.BytesIO(content)) as archive:
        if (
            DOCUMENT_XML not in archive.namelist()
            or DOCUMENT_RELS not in archive.namelist()
        ):
            return DocxPreprocessResult(content=content)

        document_root = _parse_xml(archive.read(DOCUMENT_XML), DOCUMENT_XML)
        objects = document_root.xpath(".//w:object[o:OLEObject]", namespaces=NS)
        if not objects:
            return DocxPreprocessResult(content=content)

        validate_archive(archive, OUTER_LIMITS)
        rels_root = _parse_xml(archive.read(DOCUMENT_RELS), DOCUMENT_RELS)
        relationships = _relationship_map(rels_root)
        documents, warnings, removable_ids = _process_objects(
            archive=archive,
            objects=objects,
            relationships=relationships,
        )
        drop_targets = _prune_relationships(
            document_root=document_root,
            rels_root=rels_root,
            relationships=relationships,
            removable_ids=removable_ids,
        )
        sanitized = _rewrite_docx(
            archive=archive,
            document_root=document_root,
            rels_root=rels_root,
            drop_targets=drop_targets,
        )
    return DocxPreprocessResult(sanitized, tuple(documents), tuple(warnings))


def _parse_xml(data: bytes, part_name: str) -> etree._Element:
    parser = etree.XMLParser(resolve_entities=False, no_network=True, huge_tree=False)
    try:
        return etree.fromstring(data, parser=parser)
    except etree.XMLSyntaxError as exc:
        raise ValueError(f"invalid XML in DOCX part {part_name}: {exc}") from exc


def _relationship_map(rels_root: etree._Element) -> dict[str, etree._Element]:
    return {
        rel.get("Id", ""): rel
        for rel in rels_root.findall(f"{{{PKG_REL_NS}}}Relationship")
        if rel.get("Id")
    }


def _process_objects(
    *,
    archive: zipfile.ZipFile,
    objects: Iterable[etree._Element],
    relationships: dict[str, etree._Element],
) -> tuple[list[EmbeddedDocument], list[str], set[str]]:
    documents: list[EmbeddedDocument] = []
    warnings: list[str] = []
    removable_ids: set[str] = set()
    total_bytes = 0

    for index, obj in enumerate(objects, start=1):
        ole = obj.find(f".//{{{O_NS}}}OLEObject")
        ole_id = ole.get(f"{{{R_NS}}}id", "") if ole is not None else ""
        removable_ids.update(_preview_relationship_ids(obj))
        if ole_id:
            removable_ids.add(ole_id)

        document, warning = _extract_embedded_docx(
            archive=archive,
            relationships=relationships,
            ole=ole,
            object_index=index,
            document_count=len(documents),
            total_bytes=total_bytes,
        )
        if document is not None:
            documents.append(document)
            total_bytes += len(document.content)
            _replace_object_with_marker(obj, document.marker)
        else:
            _remove_object(obj)
        if warning:
            warnings.append(warning)

    return documents, warnings, removable_ids


def _extract_embedded_docx(
    *,
    archive: zipfile.ZipFile,
    relationships: dict[str, etree._Element],
    ole: etree._Element | None,
    object_index: int,
    document_count: int,
    total_bytes: int,
) -> tuple[EmbeddedDocument | None, str]:
    if ole is None or ole.get("Type", "Embed").lower() != "embed":
        return (
            None,
            f"embedded object {object_index} skipped: only embedded objects are allowed",
        )
    if document_count >= MAX_EMBEDDED_DOCUMENTS:
        return (
            None,
            f"embedded object {object_index} skipped: document count limit reached",
        )

    relationship = relationships.get(ole.get(f"{{{R_NS}}}id", ""))
    target, reason = _ole_target(relationship)
    if not target:
        return None, f"embedded object {object_index} skipped: {reason}"

    try:
        info = archive.getinfo(target)
    except KeyError:
        return None, f"embedded object {object_index} skipped: package part is missing"
    if info.file_size > MAX_EMBEDDED_DOCUMENT_BYTES:
        return None, f"embedded object {object_index} skipped: file size limit exceeded"
    if total_bytes + info.file_size > MAX_EMBEDDED_TOTAL_BYTES:
        return (
            None,
            f"embedded object {object_index} skipped: total size limit exceeded",
        )

    payload = archive.read(info)
    safe, reason = is_safe_embedded_docx(payload)
    if not safe:
        return None, f"embedded object {object_index} skipped: {reason}"

    marker = f"[[WEKNORA_EMBEDDED_DOCUMENT_{object_index}]]"
    name = f"embedded-document-{object_index}.docx"
    return EmbeddedDocument(marker, name, "docx", payload), ""


def _ole_target(relationship: etree._Element | None) -> tuple[str, str]:
    if relationship is None or not relationship.get("Type", "").endswith(
        OLE_REL_SUFFIX
    ):
        return "", "OLE relationship is invalid"
    if relationship.get("TargetMode", "").lower() == "external":
        return "", "external OLE links are not allowed"

    target = unquote(relationship.get("Target", ""))
    if not target or target.startswith(("/", "\\")) or "\\" in target:
        return "", "OLE target path is invalid"
    normalized = posixpath.normpath(posixpath.join("word", target))
    if not normalized.startswith("word/embeddings/"):
        return "", "OLE target is outside word/embeddings"
    return normalized, ""


def _preview_relationship_ids(obj: etree._Element) -> set[str]:
    return {
        node.get(f"{{{R_NS}}}id", "")
        for node in obj.findall(f".//{{{V_NS}}}imagedata")
        if node.get(f"{{{R_NS}}}id")
    }


def _replace_object_with_marker(obj: etree._Element, marker: str) -> None:
    parent = obj.getparent()
    if parent is None:
        return
    index = parent.index(obj)
    parent.remove(obj)
    marker_node = etree.Element(f"{{{W_NS}}}t")
    marker_node.text = marker
    parent.insert(index, marker_node)


def _remove_object(obj: etree._Element) -> None:
    parent = obj.getparent()
    if parent is not None:
        parent.remove(obj)


def _document_relationship_ids(document_root: etree._Element) -> set[str]:
    ids: set[str] = set()
    for node in document_root.iter():
        for attr, value in node.attrib.items():
            if etree.QName(attr).namespace == R_NS and value:
                ids.add(value)
    return ids


def _prune_relationships(
    *,
    document_root: etree._Element,
    rels_root: etree._Element,
    relationships: dict[str, etree._Element],
    removable_ids: set[str],
) -> set[str]:
    referenced_ids = _document_relationship_ids(document_root)
    pruned_ids = removable_ids - referenced_ids
    target_by_id = {
        rel_id: _internal_target(rel)
        for rel_id, rel in relationships.items()
        if rel_id in pruned_ids
    }
    for rel_id in pruned_ids:
        rel = relationships.get(rel_id)
        if rel is not None and rel.getparent() is rels_root:
            rels_root.remove(rel)

    remaining_targets = {
        _internal_target(rel)
        for rel in rels_root.findall(f"{{{PKG_REL_NS}}}Relationship")
    }
    return {
        target
        for target in target_by_id.values()
        if target and target not in remaining_targets
    }


def _internal_target(relationship: etree._Element) -> str:
    if relationship.get("TargetMode", "").lower() == "external":
        return ""
    target = unquote(relationship.get("Target", ""))
    if not target or target.startswith(("/", "\\")) or "\\" in target:
        return ""
    normalized = posixpath.normpath(posixpath.join("word", target))
    if normalized == ".." or normalized.startswith("../"):
        return ""
    return normalized


def _rewrite_docx(
    *,
    archive: zipfile.ZipFile,
    document_root: etree._Element,
    rels_root: etree._Element,
    drop_targets: set[str],
) -> bytes:
    replacements = {
        DOCUMENT_XML: etree.tostring(
            document_root,
            encoding="UTF-8",
            xml_declaration=True,
            standalone=True,
        ),
        DOCUMENT_RELS: etree.tostring(
            rels_root,
            encoding="UTF-8",
            xml_declaration=True,
            standalone=True,
        ),
    }
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", allowZip64=True) as rewritten:
        for info in archive.infolist():
            if info.filename in drop_targets:
                continue
            data = replacements.get(info.filename)
            if data is None:
                data = archive.read(info)
            rewritten.writestr(info, data)
    return output.getvalue()
