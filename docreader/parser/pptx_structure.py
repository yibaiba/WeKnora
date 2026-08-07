"""PPTX OOXML structure mapped onto finalized MarkItDown Markdown."""

from __future__ import annotations

import html
import logging
import re
import zipfile
from dataclasses import dataclass
from xml.etree import ElementTree

from docreader.models.document import Document, StructureBlock
from docreader.parser.pptx_ooxml import (
    PPTXShape,
    PPTXSlide,
    read_pptx_slides,
)


_MARKDOWN_PREFIX = re.compile(r"^(?:#{1,6}\s+|[-*+]\s+|\d+[.)]\s+)")
_TABLE_SEPARATOR = re.compile(r"^:?-{3,}:?$")

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class _MarkdownLine:
    text: str
    start: int
    end: int


def attach_pptx_structure(content: bytes, document: Document) -> Document:
    """Attach OOXML-backed hints without changing the parser's Markdown."""
    try:
        slides = read_pptx_slides(content)
    except (zipfile.BadZipFile, KeyError, ValueError, ElementTree.ParseError) as exc:
        logger.warning("PPTX structure metadata is invalid: %s", exc)
        metadata = dict(document.metadata)
        metadata["semantic_structure_status"] = "pptx_metadata_invalid"
        return document.model_copy(update={"metadata": metadata})
    blocks = _PPTXStructureMapper(document.content).map(slides)
    return document.model_copy(update={"structure_blocks": blocks})


class _PPTXStructureMapper:
    def __init__(self, markdown: str):
        self.lines = _markdown_lines(markdown)
        self.cursor = 0
        self.blocks: list[StructureBlock] = []
        self.sequences: dict[str, int] = {}
        self.parent_id = ""

    def map(self, slides: tuple[PPTXSlide, ...]) -> list[StructureBlock]:
        for slide in slides:
            self.parent_id = ""
            self._map_slide_marker(slide.number)
            for shape in slide.shapes:
                if shape.kind == "title":
                    self._map_title(shape)
                elif shape.kind == "text":
                    self._map_text(shape)
                elif shape.kind == "table":
                    self._map_table(shape.table)
        return self.blocks

    def _map_slide_marker(self, number: int) -> None:
        marker = f"<!-- Slide number: {number} -->"
        line = self._find_line(marker, preserve_prefix=True)
        if line is None:
            return
        self._add(
            "page_region", line.start, line.end, atomic=False,
            block_id=f"pptx-slide-{number:04d}", parent_id="",
        )

    def _map_title(self, shape: PPTXShape) -> None:
        for index, paragraph in enumerate(shape.paragraphs):
            line = self._find_line(paragraph.text)
            if line is None:
                continue
            if index == 0:
                block_id = self._next_id("heading")
                self._add(
                    "heading", line.start, line.end, atomic=True,
                    block_id=block_id, parent_id="", section_depth=1,
                )
                self.parent_id = block_id
            else:
                self._add("paragraph", line.start, line.end, atomic=False)

    def _map_text(self, shape: PPTXShape) -> None:
        for paragraph in shape.paragraphs:
            line = self._find_line(paragraph.text)
            if line is None:
                continue
            kind = "list_item" if paragraph.is_list else "paragraph"
            self._add(kind, line.start, line.end, atomic=paragraph.is_list)

    def _map_table(self, rows: tuple[tuple[str, ...], ...]) -> None:
        indexes = self._find_table_lines(rows)
        if not indexes:
            return
        table_id = self._next_id("table")
        header, separator = self.lines[indexes[0]], self.lines[indexes[1]]
        self._add(
            "table_header", header.start, separator.end,
            atomic=True, table_id=table_id,
        )
        for index in indexes[2:]:
            line = self.lines[index]
            self._add(
                "table_row", line.start, line.end,
                atomic=True, table_id=table_id,
            )
        self.cursor = max(self.cursor, self.lines[indexes[-1]].end)

    def _find_line(
        self, source: str, *, preserve_prefix: bool = False
    ) -> _MarkdownLine | None:
        expected = _normalize_text(source)
        for line in self.lines:
            if line.start < self.cursor or _table_cells(line.text):
                continue
            value = line.text.strip() if preserve_prefix else _strip_markdown_prefix(line.text)
            if _normalize_text(value) == expected:
                self.cursor = line.end
                return line
        return None

    def _find_table_lines(
        self, rows: tuple[tuple[str, ...], ...]
    ) -> list[int]:
        expected = [[_normalize_text(cell) for cell in row] for row in rows]
        for index, line in enumerate(self.lines):
            if line.start < self.cursor or _table_cells(line.text) != expected[0]:
                continue
            if index + 1 >= len(self.lines) or not _is_table_separator(
                self.lines[index + 1].text
            ):
                continue
            matches = [index, index + 1]
            for row_index, row in enumerate(expected[1:], start=index + 2):
                if row_index >= len(self.lines) or _table_cells(
                    self.lines[row_index].text
                ) != row:
                    break
                matches.append(row_index)
            if len(matches) == len(expected) + 1:
                return matches
        return []

    def _add(
        self,
        kind: str,
        start: int,
        end: int,
        *,
        atomic: bool,
        block_id: str = "",
        parent_id: str | None = None,
        section_depth: int = 0,
        table_id: str = "",
    ) -> None:
        self.blocks.append(StructureBlock(
            id=block_id or self._next_id(kind), kind=kind,
            start=start, end=end,
            parent_id=self.parent_id if parent_id is None else parent_id,
            section_depth=section_depth, table_id=table_id,
            atomic=atomic, confidence="high",
        ))

    def _next_id(self, kind: str) -> str:
        self.sequences[kind] = self.sequences.get(kind, 0) + 1
        return f"pptx-{kind}-{self.sequences[kind]:04d}"


def _markdown_lines(markdown: str) -> list[_MarkdownLine]:
    result: list[_MarkdownLine] = []
    offset = 0
    while offset < len(markdown):
        newline = markdown.find("\n", offset)
        end = len(markdown) if newline < 0 else newline + 1
        result.append(_MarkdownLine(markdown[offset:end], offset, end))
        offset = end
    return result


def _strip_markdown_prefix(value: str) -> str:
    return _MARKDOWN_PREFIX.sub("", value.strip())


def _normalize_text(value: str) -> str:
    return " ".join(html.unescape(value).replace("\\|", "|").split())


def _table_cells(value: str) -> list[str]:
    stripped = value.strip()
    if not stripped.startswith("|") or not stripped.endswith("|"):
        return []
    cells = re.split(r"(?<!\\)\|", stripped.strip("|"))
    return [_normalize_text(cell) for cell in cells]


def _is_table_separator(value: str) -> bool:
    cells = _table_cells(value)
    return bool(cells) and all(_TABLE_SEPARATOR.match(cell) for cell in cells)
