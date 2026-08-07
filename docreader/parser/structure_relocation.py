"""Deterministic relocation of parser hints across Markdown pipeline stages."""

from __future__ import annotations

from dataclasses import dataclass

from docreader.models.document import StructureBlock


@dataclass(frozen=True)
class StructureRelocationResult:
    blocks: tuple[StructureBlock, ...]
    rejected_count: int
    reason_codes: tuple[str, ...]


@dataclass(frozen=True)
class _NormalizedText:
    value: str
    starts: tuple[int, ...]
    ends: tuple[int, ...]


def relocate_structure_blocks(
    source: str,
    final: str,
    blocks: list[StructureBlock],
) -> StructureRelocationResult:
    """Relocate exact or uniquely whitespace-normalized source fragments."""
    ordered = sorted(blocks, key=lambda block: (block.start, block.end))
    relocated: list[StructureBlock] = []
    reasons: list[str] = []
    cursor = 0
    for block in ordered:
        if block.start < 0 or block.end <= block.start or block.end > len(source):
            _append_reason(reasons, "pipeline_hint_source_range_invalid")
            continue
        fragment = source[block.start : block.end]
        match, code = _find_fragment(final, fragment, cursor, block.start)
        if code:
            _append_reason(reasons, code)
            continue
        start, end = match
        relocated.append(block.model_copy(update={"start": start, "end": end}))
        cursor = end
    valid, relation_rejections = _filter_relations(relocated)
    for code in relation_rejections:
        _append_reason(reasons, code)
    return StructureRelocationResult(
        blocks=tuple(valid),
        rejected_count=len(blocks) - len(valid),
        reason_codes=tuple(reasons),
    )


def _find_fragment(
    final: str,
    fragment: str,
    cursor: int,
    preferred: int,
) -> tuple[tuple[int, int], str]:
    if not fragment.strip():
        return (0, 0), "pipeline_hint_source_empty"
    if preferred >= cursor and final.startswith(fragment, preferred):
        return (preferred, preferred + len(fragment)), ""
    exact = _find_all(final, fragment, cursor)
    if len(exact) == 1:
        return (exact[0], exact[0] + len(fragment)), ""
    if len(exact) > 1:
        return (0, 0), "pipeline_hint_source_ambiguous"
    return _find_normalized_fragment(final, fragment, cursor)


def _find_normalized_fragment(
    final: str,
    fragment: str,
    cursor: int,
) -> tuple[tuple[int, int], str]:
    normalized_final = _normalize(final)
    normalized_fragment = _normalize(fragment)
    if not normalized_fragment.value:
        return (0, 0), "pipeline_hint_source_empty"
    matches: list[tuple[int, int]] = []
    for position in _find_all(normalized_final.value, normalized_fragment.value, 0):
        start = normalized_final.starts[position]
        if start < cursor:
            continue
        last = position + len(normalized_fragment.value) - 1
        matches.append((start, normalized_final.ends[last]))
    if len(matches) == 1:
        return matches[0], ""
    if len(matches) > 1:
        return (0, 0), "pipeline_hint_normalized_ambiguous"
    return (0, 0), "pipeline_hint_source_unmatched"


def _find_all(value: str, needle: str, start: int) -> list[int]:
    if not needle or start < 0 or start > len(value):
        return []
    matches: list[int] = []
    cursor = start
    while cursor <= len(value) - len(needle):
        position = value.find(needle, cursor)
        if position < 0:
            break
        matches.append(position)
        cursor = position + 1
    return matches


def _normalize(value: str) -> _NormalizedText:
    characters: list[str] = []
    starts: list[int] = []
    ends: list[int] = []
    index = 0
    while index < len(value):
        start = index
        if value[index].isspace():
            while index < len(value) and value[index].isspace():
                index += 1
            characters.append(" ")
        else:
            characters.append(value[index])
            index += 1
        starts.append(start)
        ends.append(index)
    return _NormalizedText("".join(characters), tuple(starts), tuple(ends))


def _filter_relations(
    blocks: list[StructureBlock],
) -> tuple[list[StructureBlock], list[str]]:
    accepted: list[StructureBlock] = []
    seen: dict[str, str] = {}
    reasons: list[str] = []
    for block in blocks:
        if not block.id or block.id in seen:
            _append_reason(reasons, "pipeline_hint_id_invalid")
            continue
        if block.parent_id and seen.get(block.parent_id) != "heading":
            _append_reason(reasons, "pipeline_hint_parent_invalid")
            continue
        seen[block.id] = block.kind
        accepted.append(block)
    return accepted, reasons


def _append_reason(reasons: list[str], value: str) -> None:
    if value not in reasons:
        reasons.append(value)
