#!/usr/bin/env bash
# gen-coverage-badge.sh renders a self-hosted, shields.io-"flat"-style SVG
# badge from a real `go test -coverprofile` run's own total coverage
# percentage -- no external service, no API token, matching this repo's
# existing CI-status badge (README.md's own GitHub Actions workflow-badge
# link), which is also self-hosted rather than routed through a
# third-party badge host.
#
# Usage: gen-coverage-badge.sh <coverage.out> <output.svg>
#
# Width/color logic mirrors shields.io's own long-standing flat template
# closely enough to look right alongside it (same font metrics, same
# color thresholds for a "coverage" label), but is a from-scratch
# reimplementation -- this script has no dependency on shields.io itself,
# so it works with no network access at all, offline-safe the same way
# `go test` itself already is.
set -euo pipefail

profile="${1:?usage: gen-coverage-badge.sh <coverage.out> <output.svg>}"
out="${2:?usage: gen-coverage-badge.sh <coverage.out> <output.svg>}"

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
# project's own coverage badge does.
color="#e05d44" # red
awk_ge() { awk -v a="$1" -v b="$2" 'BEGIN{exit !(a>=b)}'; }
if awk_ge "$pct_raw" 90; then color="#4c1"        # brightgreen
elif awk_ge "$pct_raw" 80; then color="#97CA00"   # green
elif awk_ge "$pct_raw" 70; then color="#a4a61d"   # yellowgreen
elif awk_ge "$pct_raw" 60; then color="#dfb317"   # yellow
elif awk_ge "$pct_raw" 50; then color="#fe7d37"   # orange
fi

label="coverage"
message=$(printf '%.1f%%' "$pct_raw")

# Approximate Verdana-11px advance width, generously overestimated
# (7.8px/char plus 14px of padding on each side) rather than exactly
# matched: an SVG <text> with no textLength just centers via
# text-anchor="middle" and can only run past its own rect if the box is
# too *narrow* for the real rendered glyphs (font substitution differs
# across renderers/OSes -- GitHub's own README preview, a browser with
# no real Verdana installed, this script's own verification via
# headless Chromium, all substitute something a little different), so
# erring wide costs a few px of quiet padding, not clipped text.
char_w=7.8
label_w=$(awk -v n="${#label}" -v cw="$char_w" 'BEGIN{printf "%d", n*cw+28}')
message_w=$(awk -v n="${#message}" -v cw="$char_w" 'BEGIN{printf "%d", n*cw+28}')
total_w=$((label_w + message_w))
label_mid=$((label_w / 2))
message_mid=$((label_w + message_w / 2))

cat > "$out" <<SVG
<svg xmlns="http://www.w3.org/2000/svg" width="${total_w}" height="20" role="img" aria-label="${label}: ${message}">
  <linearGradient id="s" x2="0" y2="100%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>
  <clipPath id="r">
    <rect width="${total_w}" height="20" rx="3" fill="#fff"/>
  </clipPath>
  <g clip-path="url(#r)">
    <rect width="${label_w}" height="20" fill="#555"/>
    <rect x="${label_w}" width="${message_w}" height="20" fill="${color}"/>
    <rect width="${total_w}" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" text-rendering="geometricPrecision" font-size="110">
    <text x="${label_mid}0" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)">${label}</text>
    <text x="${label_mid}0" y="140" transform="scale(.1)">${label}</text>
    <text x="${message_mid}0" y="150" fill="#010101" fill-opacity=".3" transform="scale(.1)">${message}</text>
    <text x="${message_mid}0" y="140" transform="scale(.1)">${message}</text>
  </g>
</svg>
SVG

echo "wrote ${out} (${label}: ${message}, color ${color})"
