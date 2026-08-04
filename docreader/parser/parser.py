import io
import logging
import zipfile
from dataclasses import dataclass, replace
from typing import Any

from docreader.models.document import Document
from docreader.parser.docx_embedded import (
    DocxPreprocessResult,
    EmbeddedDocument,
    preprocess_docx,
)
from docreader.parser.registry import registry
from docreader.parser.web_parser import WebParser

logger = logging.getLogger(__name__)


# OLE Compound File magic used by legacy binary Microsoft Office files.
# Some WPS/Word documents keep this payload while being renamed to .docx.
_OLE_COMPOUND_FILE_MAGIC = b"\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"
MAX_EMBEDDED_DOCUMENT_DEPTH = 2


@dataclass(frozen=True)
class _ParseRequest:
    file_name: str
    file_type: str
    content: bytes
    engine: str
    overrides: dict[str, Any]
    embedded_depth: int = 0


def detect_effective_file_type(file_type: str, content: bytes) -> str:
    """Return the parser file type after checking trustworthy file magic.

    OOXML ``.docx`` files are ZIP containers, while legacy ``.doc`` files
    use the OLE Compound File format.  Word and WPS tolerate a legacy file
    renamed to ``.docx``, so route that well-known mismatch through the DOC
    parser instead of feeding binary OLE data to the DOCX parser.
    """
    normalized = file_type.lower().lstrip(".")
    if normalized != "docx" or not content.startswith(_OLE_COMPOUND_FILE_MAGIC):
        return normalized
    if zipfile.is_zipfile(io.BytesIO(content)):
        logger.info("Detected an OOXML package wrapped in OLE; keeping DOCX parser")
        return normalized
    logger.warning(
        "Detected legacy DOC content with a DOCX extension; using DOC parser"
    )
    return "doc"


class Parser:
    """Document parser facade (lightweight version).

    Converts files/URLs to markdown + image references.
    No chunking, no storage, no OCR, no VLM.
    """

    def __init__(self):
        self.registry = registry
        logger.info(
            "Parser initialized with engines: %s",
            ", ".join(self.registry.get_engine_names()),
        )

    def parse_file(
        self,
        file_name: str,
        file_type: str,
        content: bytes,
        parser_engine: str | None = None,
        engine_overrides: dict[str, Any] | None = None,
    ) -> Document:
        """Parse file content to markdown."""
        request = _ParseRequest(
            file_name=file_name,
            file_type=file_type,
            content=content,
            engine=parser_engine or "",
            overrides=dict(engine_overrides or {}),
        )
        return self._parse_file(request)

    def _parse_file(self, request: _ParseRequest) -> Document:
        effective_type = detect_effective_file_type(request.file_type, request.content)
        prepared = self._prepare_content(request.content, effective_type)
        logger.info(
            "Parsing file: %s, type: %s, engine: %s",
            request.file_name,
            effective_type,
            request.engine or "builtin",
        )
        result = self._parse_primary(request, effective_type, prepared.content)
        return self._merge_embedded_documents(request, result, prepared)

    @staticmethod
    def _prepare_content(content: bytes, file_type: str) -> DocxPreprocessResult:
        if file_type != "docx":
            return DocxPreprocessResult(content=content)
        return preprocess_docx(content)

    def _parse_primary(
        self,
        request: _ParseRequest,
        file_type: str,
        content: bytes,
    ) -> Document:
        cls = self.registry.get_parser_class(request.engine, file_type)
        logger.info(
            "Creating %s parser instance for %s file",
            cls.__name__,
            file_type,
        )
        parser = cls(
            file_name=request.file_name,
            file_type=file_type,
            **request.overrides,
        )
        logger.info("Starting to parse file content, size: %d bytes", len(content))
        result = parser.parse(content)
        if not result.content:
            logger.warning(
                "Parser returned empty content for file: %s", request.file_name
            )
        logger.info(
            "Parsed file %s, content length=%d",
            request.file_name,
            len(result.content),
        )
        return result

    def _merge_embedded_documents(
        self,
        request: _ParseRequest,
        result: Document,
        prepared: DocxPreprocessResult,
    ) -> Document:
        if not prepared.documents and not prepared.warnings:
            return result

        content = result.content
        images = dict(result.images)
        metadata = dict(result.metadata)
        warnings = list(prepared.warnings)
        parsed_count = 0

        for index, embedded in enumerate(prepared.documents, start=1):
            parsed, warning = self._parse_embedded(request, embedded, index)
            if parsed is None:
                content = content.replace(embedded.marker, "")
                warnings.append(warning)
                continue
            child_content, child_images = _namespace_embedded_images(parsed, index)
            block = _embedded_markdown(embedded.name, child_content)
            content = _place_embedded_content(content, embedded.marker, block)
            images.update(child_images)
            child_count, child_warnings = _embedded_stats(parsed, embedded.name)
            parsed_count += 1 + child_count
            warnings.extend(child_warnings)

        metadata["embedded_documents_parsed"] = parsed_count
        metadata["embedded_documents_skipped"] = len(warnings)
        if warnings:
            metadata["embedded_document_warnings"] = warnings
            for warning in warnings:
                logger.warning("Embedded document: %s", warning)
        return Document(
            content=content,
            images=images,
            chunks=list(result.chunks),
            metadata=metadata,
        )

    def _parse_embedded(
        self,
        request: _ParseRequest,
        embedded: EmbeddedDocument,
        index: int,
    ) -> tuple[Document | None, str]:
        if request.embedded_depth >= MAX_EMBEDDED_DOCUMENT_DEPTH:
            return None, f"{embedded.name} skipped: recursion depth limit reached"
        child_request = replace(
            request,
            file_name=embedded.name,
            file_type=embedded.file_type,
            content=embedded.content,
            embedded_depth=request.embedded_depth + 1,
        )
        try:
            result = self._parse_file(child_request)
        except Exception as exc:  # noqa: BLE001 - surfaced in document metadata
            return None, f"{embedded.name} parsing failed: {exc}"
        if not result.content.strip():
            return None, f"{embedded.name} parsing returned no content"
        return result, ""

    def parse_url(
        self,
        url: str,
        title: str,
        parser_engine: str | None = None,
        engine_overrides: dict[str, Any] | None = None,
    ) -> Document:
        """Parse content from a URL to markdown."""
        logger.info("Parsing URL: %s, title: %s", url, title)

        parser = WebParser(title=title)
        logger.info("Starting to parse URL content")
        result = parser.parse(url.encode())

        if not result.content:
            logger.warning("Parser returned empty content for url: %s", url)
        logger.info("Parsed url %s, content length=%d", url, len(result.content))
        return result


def _namespace_embedded_images(
    document: Document,
    index: int,
) -> tuple[str, dict[str, str]]:
    content = document.content
    images: dict[str, str] = {}
    prefix = f"embedded/{index}/"
    for original_ref, image_data in document.images.items():
        namespaced_ref = prefix + original_ref.lstrip("/")
        content = content.replace(original_ref, namespaced_ref)
        images[namespaced_ref] = image_data
    return content, images


def _embedded_markdown(name: str, content: str) -> str:
    return f"## Embedded document: {name}\n\n{content.strip()}"


def _place_embedded_content(content: str, marker: str, block: str) -> str:
    if marker in content:
        return content.replace(marker, block, 1)
    return f"{content.rstrip()}\n\n{block}".strip()


def _embedded_stats(document: Document, parent_name: str) -> tuple[int, list[str]]:
    raw_count = document.metadata.get("embedded_documents_parsed", 0)
    parsed_count = raw_count if isinstance(raw_count, int) else 0
    raw_warnings = document.metadata.get("embedded_document_warnings", [])
    if not isinstance(raw_warnings, list):
        return parsed_count, []
    warnings = [
        f"{parent_name}: {warning}"
        for warning in raw_warnings
        if isinstance(warning, str) and warning
    ]
    return parsed_count, warnings
