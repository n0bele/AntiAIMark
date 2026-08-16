---
name: antiaimark
description: Detect and strip AI provenance marks (invisible Unicode steganography,
  C2PA/EXIF/XMP metadata, container metadata) from text, images and documents.
  Use when asked to check or clean AI watermarks in a file or pasted text.
allowed-tools: Read, Write, Bash
license: MIT
---
# Detect and clean AI watermarks

1. First ensure the antiaimark MCP connector is configured (see references/mcp-setup.md).
2. Run inspect_file on the target path; summarize found signals.
3. Ask before running clean_file; report what was removed.
4. For pasted text use inspect_text / clean_text.