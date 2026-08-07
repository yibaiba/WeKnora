import json
from pathlib import Path

from function_test_loader import load_function_tests

from docreader.models.document import Document, StructureBlock
from docreader.structure import structure_blocks_from_document


FIXTURE_PATH = (
    Path(__file__).resolve().parents[2]
    / "internal"
    / "infrastructure"
    / "chunker"
    / "testdata"
    / "semantic_format_contracts.json"
)


def load_contracts() -> list[dict]:
    return json.loads(FIXTURE_PATH.read_text(encoding="utf-8"))["contracts"]


def structure_block(contract: dict, raw: dict) -> StructureBlock:
    source = contract.get("hint_source") or contract["markdown"]
    start = source.index(raw["needle"])
    values = {key: value for key, value in raw.items() if key != "needle"}
    return StructureBlock(start=start, end=start + len(raw["needle"]), **values)


def test_format_contract_serializes_optional_structure_blocks():
    for contract in load_contracts():
        source_blocks = [
            structure_block(contract, raw) for raw in contract["structure_blocks"]
        ]
        document = Document(content=contract["markdown"], structure_blocks=source_blocks)

        transported = structure_blocks_from_document(document)

        assert len(transported) == len(source_blocks), contract["format"]
        for source, result in zip(source_blocks, transported, strict=True):
            assert result.kind == source.kind
            assert result.start == source.start
            assert result.end == source.end
            assert result.context_kinds == source.context_kinds


def test_format_contract_covers_all_required_inputs():
    assert {contract["format"] for contract in load_contracts()} == {
        "docx",
        "pdf",
        "markdown",
        "html",
        "txt",
        "pptx",
    }


def load_tests(loader, tests, pattern):
    del loader, pattern
    return load_function_tests(globals(), tests)
