from __future__ import annotations

import io
import zipfile
from dataclasses import dataclass

from lxml import etree

OLE_MAGIC = bytes.fromhex("d0cf11e0a1b11ae1")
ZIP_MAGIC = bytes.fromhex("504b0304")
PE_MAGIC = b"MZ"
CONTENT_TYPES = "[Content_Types].xml"
DOCUMENT_XML = "word/document.xml"

MAX_DOCX_ENTRIES = 4096
MAX_DOCX_UNCOMPRESSED_BYTES = 512 * 1024 * 1024
MAX_EMBEDDED_ENTRIES = 2048
MAX_EMBEDDED_UNCOMPRESSED_BYTES = 100 * 1024 * 1024

CONTENT_TYPE_NS = "http://schemas.openxmlformats.org/package/2006/content-types"
SAFE_DOCX_CONTENT_TYPE = (
    "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"
)
DANGEROUS_PART_SUFFIXES = (
    ".app",
    ".bat",
    ".bash",
    ".class",
    ".cmd",
    ".com",
    ".cpl",
    ".dll",
    ".dylib",
    ".exe",
    ".hta",
    ".jar",
    ".js",
    ".jse",
    ".msi",
    ".msp",
    ".ps1",
    ".psm1",
    ".scr",
    ".sh",
    ".so",
    ".sys",
    ".vbe",
    ".vbs",
    ".wsf",
    ".wsh",
    ".zsh",
)


@dataclass(frozen=True)
class ArchiveLimits:
    max_entries: int
    max_uncompressed_bytes: int


OUTER_LIMITS = ArchiveLimits(MAX_DOCX_ENTRIES, MAX_DOCX_UNCOMPRESSED_BYTES)
EMBEDDED_LIMITS = ArchiveLimits(
    MAX_EMBEDDED_ENTRIES,
    MAX_EMBEDDED_UNCOMPRESSED_BYTES,
)


def validate_archive(archive: zipfile.ZipFile, limits: ArchiveLimits) -> None:
    infos = archive.infolist()
    if len(infos) > limits.max_entries:
        raise ValueError(f"DOCX archive has too many entries: {len(infos)}")

    names: set[str] = set()
    total_size = 0
    for info in infos:
        _validate_part_name(info.filename)
        if info.filename in names:
            raise ValueError(f"DOCX archive contains duplicate part: {info.filename}")
        if info.flag_bits & 0x1:
            raise ValueError(f"DOCX archive contains encrypted part: {info.filename}")
        names.add(info.filename)
        total_size += info.file_size
        if total_size > limits.max_uncompressed_bytes:
            raise ValueError("DOCX archive exceeds uncompressed size limit")


def is_safe_embedded_docx(payload: bytes) -> tuple[bool, str]:
    if payload.startswith(PE_MAGIC):
        return False, "executable content is not allowed"
    if not payload.startswith((ZIP_MAGIC, OLE_MAGIC)):
        return False, "content is not a supported document container"
    if not zipfile.is_zipfile(io.BytesIO(payload)):
        return False, "content is not a valid OOXML package"

    try:
        with zipfile.ZipFile(io.BytesIO(payload)) as archive:
            validate_archive(archive, EMBEDDED_LIMITS)
            names = set(archive.namelist())
            if CONTENT_TYPES not in names or DOCUMENT_XML not in names:
                return False, "only DOCX embedded documents are supported"
            if _contains_active_content(names):
                return False, "macro or active content is not allowed"
            root = _parse_content_types(archive.read(CONTENT_TYPES))
            if not _has_safe_docx_main_part(root):
                return False, "only macro-free DOCX documents are allowed"
    except (ValueError, zipfile.BadZipFile, RuntimeError) as exc:
        return False, str(exc)
    return True, ""


def _validate_part_name(name: str) -> None:
    backslash = chr(92)
    if not name or name.startswith(("/", backslash)) or backslash in name:
        raise ValueError(f"invalid DOCX part path: {name!r}")
    if ".." in name.split("/"):
        raise ValueError(f"DOCX part escapes package root: {name!r}")


def _contains_active_content(names: set[str]) -> bool:
    lowered = {name.lower() for name in names}
    return any(
        name.endswith("vbaproject.bin")
        or "/activex/" in f"/{name}"
        or name.endswith(DANGEROUS_PART_SUFFIXES)
        for name in lowered
    )


def _parse_content_types(data: bytes) -> etree._Element:
    parser = etree.XMLParser(resolve_entities=False, no_network=True, huge_tree=False)
    try:
        return etree.fromstring(data, parser=parser)
    except etree.XMLSyntaxError as exc:
        raise ValueError(f"invalid XML in DOCX part {CONTENT_TYPES}: {exc}") from exc


def _has_safe_docx_main_part(root: etree._Element) -> bool:
    for override in root.findall(f"{{{CONTENT_TYPE_NS}}}Override"):
        if override.get("PartName") != "/word/document.xml":
            continue
        return override.get("ContentType") == SAFE_DOCX_CONTENT_TYPE
    return False
