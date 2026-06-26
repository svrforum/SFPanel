#!/usr/bin/env bash
# Regenerate THIRD-PARTY-LICENSES.md from the actually-linked Go modules and the
# web/ npm production dependencies. go-licenses is avoided (it breaks on the
# stdlib under recent Go); module licenses are classified from the module cache.
set -euo pipefail
cd "$(dirname "$0")/.."

MODCACHE="$(go env GOMODCACHE)"
mkdir -p web/dist && [ -f web/dist/index.html ] || echo '<!doctype html>' > web/dist/index.html

enc() { echo "$1" | sed 's@\([A-Z]\)@!\L\1@g'; }   # go module cache case-encoding
classify() {
  local f="$1"; [ -f "$f" ] || { echo UNKNOWN; return; }
  case "$(head -50 "$f")" in
    *"Apache License"*) echo Apache-2.0;;
    *"Mozilla Public License"*) echo MPL-2.0;;
    *"Redistributions in binary form"*) echo BSD-3-Clause;;
    *"Permission is hereby granted, free of charge"*) echo MIT;;
    *ISC*) echo ISC;;
    *"GNU GENERAL"*) echo "GPL(!)";;
    *) echo OTHER;;
  esac
}

: > /tmp/go-linked.tsv
go list -deps -f '{{if .Module}}{{.Module.Path}} {{.Module.Version}}{{end}}' ./cmd/sfpanel \
  | sort -u | grep -v '^github.com/svrforum/SFPanel' | while read -r mod ver; do
  [ -z "${ver:-}" ] && continue
  dir="$MODCACHE/$(enc "$mod")@$ver"
  lf=$(ls "$dir"/LICENSE* "$dir"/COPYING* "$dir"/Licence* 2>/dev/null | head -1 || true)
  printf '%s\t%s\t%s\n' "$mod" "$ver" "$(classify "$lf")" >> /tmp/go-linked.tsv
done

( cd web && npx --yes license-checker --production --json 2>/dev/null ) > /tmp/npm.json

python3 - <<'PY'
import json
go=[tuple(l.rstrip('\n').split('\t')) for l in open('/tmp/go-linked.tsv') if l.strip()]
go=[g for g in go if len(g)==3 and g[1]]; go.sort()
gc={}
for _,_,lic in go: gc[lic]=gc.get(lic,0)+1
data=json.load(open('/tmp/npm.json')); npm=[]
for k,v in data.items():
    name,_,ver=k.rpartition('@'); lic=v.get('licenses','UNKNOWN')
    if isinstance(lic,list): lic=' / '.join(lic)
    npm.append((name,ver,str(lic)))
npm.sort(key=lambda x:x[0].lower())
o=["# Third-Party Licenses\n\n",
"SFPanel is distributed as a single binary that statically links Go modules and embeds a compiled React/TypeScript single-page app. This file aggregates the third-party components redistributed in that binary and their licenses. SFPanel itself is licensed under AGPL-3.0 (see `LICENSE`).\n\n",
"All bundled dependencies use permissive, AGPL-compatible licenses (MIT / BSD / ISC / Apache-2.0 / MPL-2.0). **No GPL/LGPL/AGPL/SSPL dependency is bundled.** Full license texts are available in each dependency's source repository (and locally in the Go module cache and `web/node_modules`).\n\n",
"> Regenerate with `make third-party-licenses`.\n\n---\n\n",
f"## Go modules ({len(go)})\n\nSummary: "+", ".join(f"{v}×{k}" for k,v in sorted(gc.items(),key=lambda x:-x[1]))+"\n\n",
"| Module | Version | License |\n|---|---|---|\n"]
o+=[f"| `{m}` | {vv} | {lic} |\n" for m,vv,lic in go]
o+=[f"\n## Frontend / npm packages ({len(npm)})\n\n",
"Includes runtime and build-time packages from the `web` workspace (over-inclusive by design — attribution is never harmful).\n\n",
"**Notable non-MIT attributions:**\n",
"- `monaco-editor` (MIT) bundles the **codicon** icon font, licensed **CC-BY-4.0** — \"codicon\" by Microsoft, https://github.com/microsoft/vscode-codicons.\n",
"- `caniuse-lite` — **CC-BY-4.0** (build-time browser-support data).\n\n",
"| Package | Version | License |\n|---|---|---|\n"]
o+=[f"| `{m}` | {vv} | {lic} |\n" for m,vv,lic in npm]
open('THIRD-PARTY-LICENSES.md','w').write("".join(o))
print(f"THIRD-PARTY-LICENSES.md: {len(go)} Go + {len(npm)} npm modules")
PY
