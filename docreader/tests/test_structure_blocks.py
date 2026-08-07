from function_test_loader import load_function_tests

from docreader.models.document import Document, StructureBlock
from docreader.structure import structure_blocks_from_document


def test_structure_blocks_are_optional():
    assert structure_blocks_from_document(Document(content="plain")) == []


def test_structure_blocks_map_to_transport_message():
    document = Document(
        content="# Heading\nBody",
        structure_blocks=[
            StructureBlock(
                id="heading-1",
                kind="heading",
                start=0,
                end=9,
                section_depth=1,
                atomic=True,
                confidence="high",
                context_kinds=["section"],
            )
        ],
    )

    blocks = structure_blocks_from_document(document)

    assert len(blocks) == 1
    assert blocks[0].kind == "heading"
    assert blocks[0].id == "heading-1"
    assert blocks[0].section_depth == 1
    assert blocks[0].context_kinds == ["section"]


def load_tests(loader, tests, pattern):
    del loader, pattern
    return load_function_tests(globals(), tests)
