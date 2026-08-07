"""Public parser exports, loaded only when a format is requested."""

from importlib import import_module


_EXPORTS = {
    "Docx2Parser": ("docx2_parser", "Docx2Parser"),
    "DocParser": ("doc_parser", "DocParser"),
    "PDFParser": ("pdf_parser", "PDFParser"),
    "MarkdownParser": ("markdown_parser", "MarkdownParser"),
    "ImageParser": ("image_parser", "ImageParser"),
    "WebParser": ("web_parser", "WebParser"),
    "Parser": ("parser", "Parser"),
    "ExcelParser": ("excel_parser", "ExcelParser"),
    "HTMLParser": ("html_parser", "HTMLParser"),
    "ParserEngineRegistry": ("registry", "ParserEngineRegistry"),
    "registry": ("registry", "registry"),
}

# Export public classes and modules
__all__ = [
    "Docx2Parser",
    "DocParser",
    "PDFParser",
    "MarkdownParser",
    "ImageParser",
    "WebParser",
    "Parser",
    "ExcelParser",
    "HTMLParser",
    "ParserEngineRegistry",
    "registry",
]


def __getattr__(name: str):
    """Resolve public classes lazily so one format does not import every backend."""
    target = _EXPORTS.get(name)
    if target is None:
        raise AttributeError(f"module {__name__!r} has no attribute {name!r}")
    module_name, attribute = target
    value = getattr(import_module(f"{__name__}.{module_name}"), attribute)
    globals()[name] = value
    return value
