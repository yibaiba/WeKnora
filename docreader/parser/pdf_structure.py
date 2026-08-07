"""PDF-native page and layout hints mapped to finalized Markdown."""

from __future__ import annotations

import re
from dataclasses import dataclass
from typing import Iterable

from docreader.models.document import StructureBlock


_HEADING = re.compile(r"^(#{1,6})\s+\S")
_LIST_ITEM = re.compile(r"^\s*(?:[-*+]|\d+[.)])\s+\S")
_IMAGE = re.compile(r"^\s*!\[[^]]*]\([^)]+\)\s*$")
_CAPTION = re.compile(
    r"^\s*(?:fig(?:ure)?\.?|caption|图(?:片)?|插图)\s*\d*\s*[:：.、-]?\s*\S",
    re.IGNORECASE,
)
_TABLE_SEPARATOR_CELL = re.compile(r"^:?-{3,}:?$")


@dataclass(frozen=True)
class PDFPage:
    """One source page after text/scanned routing but before Markdown joining."""

    number: int
    content: str
    source_kind: str


@dataclass(frozen=True)
class _Line:
    text: str
    start: int
    end: int


def assemble_pdf_markdown(
    pages: Iterable[PDFPage],
    repeating_lines: frozenset[str] = frozenset(),
) -> tuple[str, list[StructureBlock]]:
    """Join routed pages and derive source-aligned native structure hints."""
    page_list = list(pages)
    pieces: list[str] = []
    for page in page_list:
        if not page.content.strip():
            continue
        pieces.append(f"\fPage {page.number}")
        pieces.append(page.content.strip())
    markdown = "\n\n".join(pieces).rstrip("\n")
    source_kinds = {page.number: page.source_kind for page in page_list}
    blocks = _PDFStructureMapper(markdown, repeating_lines, source_kinds).map()
    return markdown, blocks


class _PDFStructureMapper:
    def __init__(
        self,
        markdown: str,
        repeating_lines: frozenset[str],
        source_kinds: dict[int, str],
    ):
        self.lines = _markdown_lines(markdown)
        self.repeating_lines = repeating_lines
        self.source_kinds = source_kinds
        self.blocks: list[StructureBlock] = []
        self.heading_stack: dict[int, str] = {}
        self.sequences: dict[str, int] = {}
        self.page_number = 0

    def map(self) -> list[StructureBlock]:
        index = 0
        while index < len(self.lines):
            line = self.lines[index]
            raw_line = line.text.rstrip("\r\n")
            stripped = line.text.strip()
            if not stripped:
                index += 1
                continue
            if raw_line.startswith("\fPage "):
                self._add_page_marker(line)
                index += 1
                continue
            if stripped in self.repeating_lines:
                self._add("page_region", line.start, line.end, atomic=False, parent_id="")
                index += 1
                continue
            table_end = self._map_table(index)
            if table_end is not None:
                index = table_end
                continue
            heading = _HEADING.match(stripped)
            if heading:
                self._add_heading(line, len(heading.group(1)))
                index += 1
                continue
            if _LIST_ITEM.match(stripped):
                self._add("list_item", line.start, line.end, atomic=True)
                index += 1
                continue
            if _IMAGE.match(stripped):
                index = self._add_image(index)
                continue
            index += 1
        return self.blocks

    def _add_page_marker(self, line: _Line) -> None:
        match = re.search(r"\d+", line.text)
        self.page_number = int(match.group()) if match else self.page_number + 1
        source_kind = self.source_kinds.get(self.page_number, "text")
        suffix = "ocr" if source_kind == "scanned" else "layout"
        self._add(
            "page_region",
            line.start,
            line.end,
            atomic=False,
            parent_id="",
            block_id=f"pdf-page-{self.page_number:04d}-{suffix}",
        )

    def _map_table(self, index: int) -> int | None:
        if index + 1 >= len(self.lines):
            return None
        if not _table_cells(self.lines[index].text):
            return None
        if not _is_table_separator(self.lines[index + 1].text):
            return None
        table_id = self._next_id("table")
        self._add(
            "table_header",
            self.lines[index].start,
            self.lines[index + 1].end,
            atomic=True,
            table_id=table_id,
        )
        index += 2
        while index < len(self.lines) and _table_cells(self.lines[index].text):
            line = self.lines[index]
            self._add(
                "table_row", line.start, line.end, atomic=True, table_id=table_id
            )
            index += 1
        return index

    def _add_heading(self, line: _Line, depth: int) -> None:
        block_id = self._next_id("heading")
        parent_id = _nearest_heading(self.heading_stack, depth - 1)
        self._add(
            "heading",
            line.start,
            line.end,
            atomic=True,
            block_id=block_id,
            parent_id=parent_id,
            section_depth=depth,
        )
        self.heading_stack = {
            level: value
            for level, value in self.heading_stack.items()
            if level < depth
        }
        self.heading_stack[depth] = block_id

    def _add_image(self, index: int) -> int:
        line = self.lines[index]
        end = line.end
        contexts = ["image"]
        if index + 1 < len(self.lines) and _CAPTION.match(
            self.lines[index + 1].text.strip()
        ):
            end = self.lines[index + 1].end
            contexts.append("caption")
            index += 1
        self._add(
            "image_caption",
            line.start,
            end,
            atomic=True,
            context_kinds=contexts,
        )
        return index + 1

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
        context_kinds: list[str] | None = None,
    ) -> None:
        self.blocks.append(
            StructureBlock(
                id=block_id or self._next_id(kind),
                kind=kind,
                start=start,
                end=end,
                parent_id=(
                    _nearest_heading(self.heading_stack, 6)
                    if parent_id is None
                    else parent_id
                ),
                section_depth=section_depth,
                table_id=table_id,
                atomic=atomic,
                confidence="high",
                context_kinds=context_kinds or [],
            )
        )

    def _next_id(self, kind: str) -> str:
        self.sequences[kind] = self.sequences.get(kind, 0) + 1
        return f"pdf-{kind}-{self.sequences[kind]:04d}"


def _markdown_lines(markdown: str) -> list[_Line]:
    lines: list[_Line] = []
    offset = 0
    while offset < len(markdown):
        newline = markdown.find("\n", offset)
        end = len(markdown) if newline < 0 else newline + 1
        lines.append(_Line(markdown[offset:end], offset, end))
        offset = end
    return lines


def _table_cells(value: str) -> list[str]:
    stripped = value.strip()
    if not stripped.startswith("|") or not stripped.endswith("|"):
        return []
    cells = re.split(r"(?<!\\)\|", stripped.strip("|"))
    return [cell.strip() for cell in cells] if len(cells) >= 2 else []


def _is_table_separator(value: str) -> bool:
    cells = _table_cells(value)
    return bool(cells) and all(_TABLE_SEPARATOR_CELL.match(cell) for cell in cells)


def _nearest_heading(stack: dict[int, str], maximum: int) -> str:
    for depth in range(maximum, 0, -1):
        if stack.get(depth):
            return stack[depth]
    return ""
