import io
import re
import unittest
import zipfile
from typing import ClassVar
from unittest.mock import patch

from docreader.models.document import Document
from docreader.parser.base_parser import BaseParser
from docreader.parser.docx_embedded import preprocess_docx
from docreader.parser.docx_embedded_security import OLE_MAGIC
from docreader.parser.parser import Parser

CONTENT_TYPES_TEMPLATE = """<?xml version="1.0" encoding="UTF-8"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="bin" ContentType="application/vnd.openxmlformats-officedocument.oleObject"/>
  <Default Extension="emf" ContentType="image/x-emf"/>
  <Override PartName="/word/document.xml" ContentType="{main_type}"/>
</Types>
"""

SAFE_MAIN_TYPE = (
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
)
MACRO_MAIN_TYPE = "application/vnd.ms-word.document.macroEnabled.main+xml"

PARENT_DOCUMENT_XML = """<?xml version="1.0" encoding="UTF-8"?>
<w:document
  xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
  xmlns:o="urn:schemas-microsoft-com:office:office"
  xmlns:v="urn:schemas-microsoft-com:vml"
  xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <w:body>
    <w:p><w:r><w:t>Outer content</w:t></w:r></w:p>
    <w:p><w:r><w:object>
      <v:shape><v:imagedata r:id="rIdPreview"/></v:shape>
      <o:OLEObject Type="Embed" ProgID="Word.Document.12" r:id="rIdOle"/>
    </w:object></w:r></w:p>
  </w:body>
</w:document>
"""

PARENT_RELS_XML = """<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rIdOle"
    Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/oleObject"
    Target="embeddings/oleObject1.bin"/>
  <Relationship Id="rIdPreview"
    Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image"
    Target="media/image2.emf"/>
</Relationships>
"""


def _zip_bytes(parts: dict[str, bytes]) -> bytes:
    output = io.BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_DEFLATED) as archive:
        for name, data in parts.items():
            archive.writestr(name, data)
    return output.getvalue()


def _embedded_docx(
    *,
    macro_enabled: bool = False,
    extra_parts: dict[str, bytes] | None = None,
) -> bytes:
    main_type = MACRO_MAIN_TYPE if macro_enabled else SAFE_MAIN_TYPE
    parts = {
        "[Content_Types].xml": CONTENT_TYPES_TEMPLATE.format(
            main_type=main_type
        ).encode(),
        "word/document.xml": (
            b'<w:document xmlns:w="http://schemas.openxmlformats.org/'
            b'wordprocessingml/2006/main"><w:body><w:p><w:r>'
            b"<w:t>Inner content</w:t></w:r></w:p></w:body></w:document>"
        ),
    }
    if macro_enabled:
        parts["word/vbaProject.bin"] = b"macro"
    parts.update(extra_parts or {})
    return OLE_MAGIC + _zip_bytes(parts)


def _parent_docx(payload: bytes) -> bytes:
    return _zip_bytes(
        {
            "[Content_Types].xml": CONTENT_TYPES_TEMPLATE.format(
                main_type=SAFE_MAIN_TYPE
            ).encode(),
            "word/document.xml": PARENT_DOCUMENT_XML.encode(),
            "word/_rels/document.xml.rels": PARENT_RELS_XML.encode(),
            "word/embeddings/oleObject1.bin": payload,
            "word/media/image2.emf": b"EMF preview icon",
        }
    )


class _ArchiveTextParser(BaseParser):
    calls: ClassVar[list[str]] = []

    def parse_into_text(self, content: bytes) -> Document:
        type(self).calls.append(self.file_name)
        with zipfile.ZipFile(io.BytesIO(content)) as archive:
            xml = archive.read("word/document.xml").decode()
            if self.file_name == "outer.docx":
                self._assert_sanitized(archive, xml)
                marker = re.search(r"WEKNORA_EMBEDDED_DOCUMENT_\d+", xml)
                text = "Outer content " + (marker.group(0) if marker else "")
                return Document(content=text)
        return Document(
            content="Inner content ![](images/inner.png)",
            images={"images/inner.png": "aW1hZ2U="},
        )

    @staticmethod
    def _assert_sanitized(archive: zipfile.ZipFile, xml: str) -> None:
        assert "OLEObject" not in xml
        assert "word/embeddings/oleObject1.bin" not in archive.namelist()
        assert "word/media/image2.emf" not in archive.namelist()


class _RecordingRegistry:
    def get_parser_class(self, engine: str, file_type: str):
        if file_type != "docx":
            raise AssertionError(f"unexpected parser type: {file_type}")
        return _ArchiveTextParser


class DocxEmbeddedDocumentTest(unittest.TestCase):
    def setUp(self):
        _ArchiveTextParser.calls = []

    def test_extracts_docx_and_removes_ole_binary_and_preview(self):
        result = preprocess_docx(_parent_docx(_embedded_docx()))

        self.assertEqual(1, len(result.documents))
        self.assertEqual("docx", result.documents[0].file_type)
        self.assertEqual((), result.warnings)
        with zipfile.ZipFile(io.BytesIO(result.content)) as archive:
            names = set(archive.namelist())
            document_xml = archive.read("word/document.xml").decode()
            rels_xml = archive.read("word/_rels/document.xml.rels").decode()

        self.assertNotIn("word/embeddings/oleObject1.bin", names)
        self.assertNotIn("word/media/image2.emf", names)
        self.assertNotIn("OLEObject", document_xml)
        self.assertIn(result.documents[0].marker, document_xml)
        self.assertNotIn("rIdOle", rels_xml)
        self.assertNotIn("rIdPreview", rels_xml)

    def test_parser_merges_only_verified_embedded_docx(self):
        parser = Parser()
        parser.registry = _RecordingRegistry()

        result = parser.parse_file(
            "outer.docx",
            "docx",
            _parent_docx(_embedded_docx()),
        )

        self.assertEqual(
            ["outer.docx", "embedded-document-1.docx"], _ArchiveTextParser.calls
        )
        self.assertIn("Outer content", result.content)
        self.assertIn("Embedded document: embedded-document-1.docx", result.content)
        self.assertIn("Inner content", result.content)
        self.assertIn("embedded/1/images/inner.png", result.content)
        self.assertEqual(
            {"embedded/1/images/inner.png": "aW1hZ2U="},
            result.images,
        )
        self.assertEqual(1, result.metadata["embedded_documents_parsed"])
        self.assertEqual(0, result.metadata["embedded_documents_skipped"])

    def test_executable_disguised_as_word_is_removed_without_parsing(self):
        parser = Parser()
        parser.registry = _RecordingRegistry()

        result = parser.parse_file(
            "outer.docx",
            "docx",
            _parent_docx(b"MZ" + b"executable" * 20),
        )

        self.assertEqual(["outer.docx"], _ArchiveTextParser.calls)
        self.assertEqual(0, result.metadata["embedded_documents_parsed"])
        self.assertEqual(1, result.metadata["embedded_documents_skipped"])
        self.assertIn(
            "executable content is not allowed",
            result.metadata["embedded_document_warnings"][0],
        )

    def test_macro_enabled_docx_is_removed_without_parsing(self):
        parser = Parser()
        parser.registry = _RecordingRegistry()

        result = parser.parse_file(
            "outer.docx",
            "docx",
            _parent_docx(_embedded_docx(macro_enabled=True)),
        )

        self.assertEqual(["outer.docx"], _ArchiveTextParser.calls)
        self.assertEqual(1, result.metadata["embedded_documents_skipped"])
        self.assertIn(
            "macro or active content is not allowed",
            result.metadata["embedded_document_warnings"][0],
        )

    def test_docx_containing_script_part_is_removed_without_parsing(self):
        parser = Parser()
        parser.registry = _RecordingRegistry()
        payload = _embedded_docx(extra_parts={"word/embeddings/setup.js": b"run()"})

        result = parser.parse_file(
            "outer.docx",
            "docx",
            _parent_docx(payload),
        )

        self.assertEqual(["outer.docx"], _ArchiveTextParser.calls)
        self.assertEqual(1, result.metadata["embedded_documents_skipped"])
        self.assertIn(
            "macro or active content is not allowed",
            result.metadata["embedded_document_warnings"][0],
        )

    def test_oversized_embedded_document_is_removed_without_parsing(self):
        parser = Parser()
        parser.registry = _RecordingRegistry()

        with patch(
            "docreader.parser.docx_embedded.MAX_EMBEDDED_DOCUMENT_BYTES",
            32,
        ):
            result = parser.parse_file(
                "outer.docx",
                "docx",
                _parent_docx(_embedded_docx()),
            )

        self.assertEqual(["outer.docx"], _ArchiveTextParser.calls)
        self.assertEqual(1, result.metadata["embedded_documents_skipped"])
        self.assertIn(
            "file size limit exceeded",
            result.metadata["embedded_document_warnings"][0],
        )

    def test_nested_rejection_is_visible_on_parent_result(self):
        parser = Parser()
        parser.registry = _RecordingRegistry()
        nested_docx = _parent_docx(b"MZ" + b"nested executable")

        result = parser.parse_file(
            "outer.docx",
            "docx",
            _parent_docx(nested_docx),
        )

        self.assertEqual(
            ["outer.docx", "embedded-document-1.docx"], _ArchiveTextParser.calls
        )
        self.assertEqual(1, result.metadata["embedded_documents_parsed"])
        self.assertEqual(1, result.metadata["embedded_documents_skipped"])
        self.assertIn(
            "executable content is not allowed",
            result.metadata["embedded_document_warnings"][0],
        )


if __name__ == "__main__":
    unittest.main()
