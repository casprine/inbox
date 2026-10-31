#!/usr/bin/env python3
"""Dev helper: splice YAML fragments into api/openapi.yaml.
usage: openapi_add.py paths.yaml [schemas.yaml] [--codes a,b,c] [--tag name]
paths.yaml is inserted before the top-level `components:`; schemas.yaml is appended to
components.schemas (end of file); --codes adds Problem.code enum values (kept sorted-ish by appending)."""
import re, sys
spec = "api/openapi.yaml"
args = sys.argv[1:]
codes, tag, files = [], None, []
i = 0
while i < len(args):
    if args[i] == "--codes": codes = args[i+1].split(","); i += 2
    elif args[i] == "--tag": tag = args[i+1]; i += 2
    else: files.append(args[i]); i += 1
s = open(spec).read()
if files:
    s = s.replace("\n\ncomponents:\n", "\n" + open(files[0]).read().rstrip("\n") + "\n\ncomponents:\n", 1)
if len(files) > 1:
    s = s.rstrip("\n") + "\n" + open(files[1]).read().rstrip("\n") + "\n"
for c in codes:
    if re.search(r"^            - %s$" % re.escape(c), s, re.M): continue
    s = s.replace("            - unauthorized\n", "            - unauthorized\n            - %s\n" % c, 1)
if tag and "  - {name: %s}" % tag not in s:
    s = s.replace("  - {name: ingest}\n", "  - {name: ingest}\n  - {name: %s}\n" % tag, 1)
open(spec, "w").write(s)
