---
name: gitea-read-file
description: Use when retrieving Gitea files through atomicqms_mcp, especially saved oversized JSON responses or base64 documents.
---

# Read a Gitea file

Use `atomicqms_mcp` and `get_file_contents` for remote retrieval. Never use curl, direct Gitea requests, or credentials. An oversized result is saved in the active workspace; do not print or read the complete JSON or base64 into model context. Local `exec` is permitted for decoding and extraction only.

Saved `.txt` files are error diagnostics, not complete successful results or JSON. Inspect only bounded local queries and reconcile mutations before retrying.

For a complete DOCX, omit `start_line`, `end_line`, and `with_lines` from `get_file_contents`. For a saved response, run this locally in the workspace, replacing only the response filename; append `docx` after the filename when it is known to be a complete DOCX. Ranged or line-transformed responses are selected data, not the original full file; refetch without those options before decoding a DOCX. It accepts `structuredContent` or JSON in a text content item, rejects errors, invalid base64, and decoded data above 20 MiB, and prints only a generated local filename and completeness metadata. It never uses a server-provided filename.

```bash
python3 - 'mcp-response-EXAMPLE.json' <<'PY'
import base64, json, os, sys, tempfile

with open(sys.argv[1], 'rb') as f:
    response = json.load(f)
if response.get('isError'):
    raise SystemExit('MCP response is an error')
item = response.get('structuredContent')
if not isinstance(item, dict) or 'content' not in item:
    item = next((json.loads(c['text']) for c in response.get('content', []) if c.get('type') == 'text'), None)
if not isinstance(item, dict) or item.get('encoding') not in ('base64', 'utf-8') or not isinstance(item.get('content'), str):
    raise SystemExit('No supported file content')
partial = any(key in item for key in ('total_lines', 'start_line', 'end_line', 'truncated'))
if partial and sys.argv[2:] == ['docx']:
    raise SystemExit('Partial or transformed response; refetch the complete DOCX')
if item['encoding'] == 'base64':
    encoded = ''.join(item['content'].split())
    if len(encoded) > 4 * ((20 * 1024 * 1024 + 2) // 3):
        raise SystemExit('File too large')
    data = base64.b64decode(encoded, validate=True)
else:
    data = item['content'].encode('utf-8')
if len(data) > 20 * 1024 * 1024 or (not partial and 'size' in item and item['size'] != len(data)):
    raise SystemExit('Invalid file size')
fd, path = tempfile.mkstemp(prefix='gitea-decoded-', suffix='.docx' if sys.argv[2:] == ['docx'] else '', dir='.')
try:
    with os.fdopen(fd, 'wb') as f:
        f.write(data)
except BaseException:
    os.unlink(path)
    raise
print(path, 'partial/transformed' if partial else 'complete')
PY
```

For DOCX, use the generated local basename for the text output, never a server filename. Create it without overwrite and with private permissions:

```bash
(umask 077; set -C; docx-review --textconv './gitea-decoded-EXAMPLE.docx' > './gitea-decoded-EXAMPLE.docx.txt')
```

If conversion fails, discard the incomplete text file. Inspect only bounded matches or counts (for example `grep -n -m 10 'term' file.txt` or `grep -c 'term' file.txt`). Do not print the whole document or base64.
