#!/usr/bin/env bash
# wasm-size.sh builds a wasm main package and checks its size against the budget in a file, so the
# first-load cost of the showcase cannot grow without somebody choosing to raise the number
# (roadmap 82). It prints the raw and the gzip -9 size (what a server sends when it compresses).
#
# Usage: wasm-size.sh <package dir> <budget file>
# The budget file has two lines: "raw=<bytes>" and "gzip=<bytes>". A number over its budget fails.
# A package with a ./files directory has a second program (the file formats, run in a web worker when first
# needed); the budget file then also has "files_raw=" and "files_gzip=", checked the same way.
set -euo pipefail

pkg="${1:?usage: wasm-size.sh <package dir> <budget file>}"
budget="${2:?usage: wasm-size.sh <package dir> <budget file>}"

out="$(mktemp -d)"
trap 'rm -rf "$out"' EXIT

# -s -w: what is shipped has no symbol table or DWARF (about 2% of the file); measure that.
(cd "$pkg" && GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o "$out/main.wasm" .)
raw=$(wc -c <"$out/main.wasm" | tr -d ' ')
gz=$(gzip -9 -c "$out/main.wasm" | wc -c | tr -d ' ')

files_raw="" files_gz=""
if [ -d "$pkg/files" ]; then
	(cd "$pkg" && GOOS=js GOARCH=wasm go build -ldflags="-s -w" -o "$out/files.wasm" ./files)
	files_raw=$(wc -c <"$out/files.wasm" | tr -d ' ')
	files_gz=$(gzip -9 -c "$out/files.wasm" | wc -c | tr -d ' ')
fi

want() { sed -n "s/^$1=//p" "$budget" | head -1; }
max_raw=$(want raw)
max_gz=$(want gzip)
[ -n "$max_raw" ] && [ -n "$max_gz" ] || { echo "wasm-size: $budget needs raw= and gzip= lines" >&2; exit 2; }

status=0
report() { # name size max
	if [ "$2" -gt "$3" ]; then
		echo "wasm-size: $1 $2 bytes is over the budget of $3 (raise it in $budget only on purpose)" >&2
		status=1
	else
		echo "wasm-size: $1 $2 bytes, budget $3 ($(( ($3 - $2) * 100 / $3 ))% left)"
	fi
}
report raw "$raw" "$max_raw"
report gzip "$gz" "$max_gz"
if [ -n "$files_raw" ]; then
	max_fraw=$(want files_raw)
	max_fgz=$(want files_gzip)
	[ -n "$max_fraw" ] && [ -n "$max_fgz" ] || { echo "wasm-size: $budget needs files_raw= and files_gzip= lines (the package has a files program)" >&2; exit 2; }
	report files-raw "$files_raw" "$max_fraw"
	report files-gzip "$files_gz" "$max_fgz"
fi
exit $status
