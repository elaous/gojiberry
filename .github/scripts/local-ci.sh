#!/usr/bin/env bash
# local-ci.sh runs the CI jobs' `make` steps on this machine, in the workflow's own order, so the
# gate can be run where there are no hosted runners (or no minutes left). It reads the steps from
# .github/workflows/ci.yml (every `run: make <target>` line of every job, each target once), so it cannot
# drift from the workflow: add a step there and it runs here.
#
#   .github/scripts/local-ci.sh              all steps, stop at the first failure
#   .github/scripts/local-ci.sh -k           keep going and list every failure at the end
#   .github/scripts/local-ci.sh lint            only the named targets, in the workflow's order
#
# LOCAL_CI_SKIP is a space-separated list of targets to leave out (default: coverage-badge, which
# needs the whole test suite a second time; the badge is updated by hand at a full run). A short report goes to
# local-ci-report.txt (ignored by git): when, which commit, each step with its time and result.
# LOCAL_CI_LIST=1 prints the steps it would run and stops. It needs what the steps need: Go, and for steps that drive a browser a Chromium (set CHROMIUM if it is not on PATH).
set -uo pipefail

cd "$(git rev-parse --show-toplevel)"
workflow=.github/workflows/ci.yml
keep_going=0
if [ "${1:-}" = "-k" ]; then keep_going=1; shift; fi
only=("$@")
skip=" ${LOCAL_CI_SKIP-coverage-badge} "

# every `run: make <target>` line of every job, in file order, each target once (generate runs in two jobs)
mapfile -t targets < <(awk '
	/^[ ]+run: make / { sub(/^[ ]+run: make /, ""); if (!seen[$0]++) print }
' "$workflow")

if [ "${#targets[@]}" -eq 0 ]; then
	echo "local-ci: no 'run: make' steps found in $workflow" >&2
	exit 2
fi

if [ -n "${LOCAL_CI_LIST:-}" ]; then
	printf '%s\n' "${targets[@]}"
	exit 0
fi

report=local-ci-report.txt
{
	echo "local CI $(date -u +%Y-%m-%dT%H:%M:%SZ) on $(git rev-parse --short HEAD)$(git diff --quiet || echo ' (uncommitted changes)')"
} >"$report"

failed=()
ran=0
for t in "${targets[@]}"; do
	if [ "${#only[@]}" -gt 0 ]; then
		match=0
		for o in "${only[@]}"; do [ "$o" = "$t" ] && match=1; done
		[ "$match" -eq 1 ] || continue
	elif [[ "$skip" == *" $t "* ]]; then
		echo "local-ci: skipping $t" | tee -a "$report"
		continue
	fi
	echo "local-ci: make $t"
	start=$(date +%s)
	if make "$t"; then
		res=ok
	else
		res=FAILED
		failed+=("$t")
	fi
	ran=$((ran + 1))
	printf '%-24s %-7s %ss\n' "$t" "$res" "$(($(date +%s) - start))" | tee -a "$report"
	if [ "$res" = FAILED ] && [ "$keep_going" -eq 0 ]; then
		break
	fi
done

if [ "$ran" -eq 0 ]; then
	echo "local-ci: nothing matched" | tee -a "$report" >&2
	exit 2
fi
if [ "${#failed[@]}" -gt 0 ]; then
	echo "local-ci: FAILED: ${failed[*]}" | tee -a "$report"
	exit 1
fi
echo "local-ci: all $ran steps passed" | tee -a "$report"
