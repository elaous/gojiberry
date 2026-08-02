#!/usr/bin/env bash
# gen-coverage-badge.sh rewrites README.md's own coverage badge line to
# a static img.shields.io badge URL carrying this exact run's `go test
# -coverprofile` total percentage -- CI-computed on every push, but
# rendered by img.shields.io itself (a public label-drawing service
# that takes the text/color as URL parameters, no fetch back to this
# repo) rather than a self-hosted SVG served via raw.githubusercontent.com.
#
# That switch matters because gojiberry is a private repository:
# raw.githubusercontent.com only renders a private repo's file for a
# live, signed-in github.com session (a short-lived signed token GitHub
# injects when it serves the README page itself), so the previous
# self-hosted-SVG-via-raw-link approach 404'd for anyone without one --
# a stale tab, a different markdown viewer, or just enough time for the
# token to expire. The native CI-status badge above it
# (github.com/OWNER/REPO/actions/.../badge.svg) and img.shields.io
# badges both work unauthenticated regardless of repo visibility, which
# is why OnDesign's own private repo already uses this same
# img.shields.io pattern for its coverage badge (see its README) --
# this brings gojiberry's badge in line with that, but still
# CI-computed on every push instead of hand-updated.
#
# Usage: gen-coverage-badge.sh <coverage.out> <README.md>
set -euo pipefail

profile="${1:?usage: gen-coverage-badge.sh <coverage.out> <README.md>}"
readme="${2:?usage: gen-coverage-badge.sh <coverage.out> <README.md>}"

# go tool cover's own -func output ends with a "total:" summary line,
# e.g. "total:\t\t\t\t\t\t(statements)\t83.2%" -- the last field is the
# real, computed-just-now coverage percentage, not something derived by
# this script's own math.
pct_raw=$(go tool cover -func="$profile" | awk '/^total:/{print $NF}')
pct_raw=${pct_raw%\%}

# Color thresholds match shields.io's own long-established convention
# for a percentage-style badge (coverage, in this case) -- brightgreen
# at the very top, sliding down through yellow to red for a genuinely
# low number, so this badge reads at a glance the same way every other
# project's own coverage badge does. img.shields.io's /badge/ endpoint
# accepts a bare hex triple (no leading #) as the color segment.
color="e05d44" # red
awk_ge() { awk -v a="$1" -v b="$2" 'BEGIN{exit !(a>=b)}'; }
if awk_ge "$pct_raw" 90; then color="4c1"        # brightgreen
elif awk_ge "$pct_raw" 80; then color="97CA00"   # green
elif awk_ge "$pct_raw" 70; then color="a4a61d"   # yellowgreen
elif awk_ge "$pct_raw" 60; then color="dfb317"   # yellow
elif awk_ge "$pct_raw" 50; then color="fe7d37"   # orange
fi

# %25 is a literal "%" in the badge's own message segment (the URL
# itself is parsed by img.shields.io before the "%" would otherwise be
# read as a percent-encoding escape of whatever follows it).
message=$(printf '%.1f%%25' "$pct_raw")
badge_url="https://img.shields.io/badge/coverage-${message}-${color}"

# README.md's coverage badge is always exactly one "![coverage](<url>)"
# line -- replace just that URL, leaving everything else (the CI badge
# above it, surrounding blank lines) untouched.
sed -i.bak -E "s#(!\[coverage\]\()[^)]*(\))#\1${badge_url}\2#" "$readme"
rm -f "${readme}.bak"

echo "updated ${readme}'s coverage badge -> coverage: $(printf '%.1f%%' "$pct_raw") (${color})"
