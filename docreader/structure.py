"""Transport conversion for optional document structure hints."""

from docreader.proto.docreader_pb2 import StructureBlock


def structure_blocks_from_document(document) -> list[StructureBlock]:
    """Copy parser-owned hints without requiring every parser to provide them."""
    return [
        StructureBlock(**block.model_dump())
        for block in (getattr(document, "structure_blocks", None) or [])
    ]
