#!/usr/bin/env bash
# mailpit-test.sh runs the mail package's SMTP tests against real Mailpit servers
# (https://mailpit.axllent.org). It starts two of them and runs the tests against each:
#
#   1. STARTTLS with a self-signed certificate and a required sign-in (the way a real provider works)
#   2. no TLS, no sign-in, on loopback (a relay on the same host)
#
# Mailpit runs from the Docker image axllent/mailpit when a Docker daemon is available, otherwise from
# the `mailpit` binary on PATH, otherwise from a build of github.com/axllent/mailpit (go install into a
# temporary directory). MAILPIT=docker|binary forces one. It fails if any test is skipped, so a
# misconfiguration cannot pass quietly. Ports: MAILPIT_PORT_BASE (default 11025) and the next five.
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"
base="${MAILPIT_PORT_BASE:-11025}"
tls_smtp=$base tls_api=$((base + 1)) plain_smtp=$((base + 2)) plain_api=$((base + 3))
work="$(mktemp -d)"
names=() pids=()
cleanup() {
	for n in "${names[@]:-}"; do [ -n "$n" ] && docker rm -f "$n" >/dev/null 2>&1 || true; done
	for p in "${pids[@]:-}"; do [ -n "$p" ] && kill "$p" >/dev/null 2>&1 || true; done
	rm -rf "$work"
}
trap cleanup EXIT

command -v openssl >/dev/null || { echo "mailpit-test: openssl is needed to make the test certificate" >&2; exit 2; }
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:prime256v1 -nodes -keyout "$work/key.pem" -out "$work/cert.pem" \
	-days 2 -subj "/CN=localhost" -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" >/dev/null 2>&1
printf 'mailer:test-password\n' >"$work/auth.txt"
chmod 755 "$work"; chmod 644 "$work"/*.pem "$work/auth.txt" # test-only secrets; a container's user must read them

mode="${MAILPIT:-auto}"
if [ "$mode" = auto ]; then
	if docker info >/dev/null 2>&1; then mode=docker
	elif command -v mailpit >/dev/null; then mode=binary
	else mode=build; fi
fi
case "$mode" in
binary) bin="$(command -v mailpit)" ;;
build)
	GOBIN="$work/bin" go install github.com/axllent/mailpit@latest
	bin="$work/bin/mailpit"
	;;
docker) ;;
*) echo "mailpit-test: MAILPIT must be docker or binary" >&2; exit 2 ;;
esac
echo "mailpit-test: running Mailpit from: $mode"

start() { # name smtp-port api-port extra-args...
	local name=$1 smtp=$2 api=$3
	shift 3
	if [ "$mode" = docker ]; then
		docker run -d --rm --name "gj-mailpit-$name-$$" -p "127.0.0.1:$smtp:1025" -p "127.0.0.1:$api:8025" -v "$work:/certs:ro" \
			axllent/mailpit "$@" >/dev/null
		names+=("gj-mailpit-$name-$$")
	else
		# the paths in the arguments are container paths; the binary sees the real ones
		local args=()
		for a in "$@"; do args+=("${a//\/certs/$work}"); done
		"$bin" --listen "127.0.0.1:$api" --smtp "127.0.0.1:$smtp" --database "$work/$name.db" "${args[@]}" >"$work/$name.log" 2>&1 &
		pids+=($!)
	fi
	for _ in $(seq 1 100); do
		curl -fs "http://127.0.0.1:$api/api/v1/info" >/dev/null 2>&1 && return 0
		sleep 0.2
	done
	echo "mailpit-test: Mailpit ($name) did not start" >&2
	[ -f "$work/$name.log" ] && cat "$work/$name.log" >&2
	exit 1
}

start tls "$tls_smtp" "$tls_api" --smtp-tls-cert /certs/cert.pem --smtp-tls-key /certs/key.pem --smtp-auth-file /certs/auth.txt
start plain "$plain_smtp" "$plain_api"

fail=0
run() { # label, env...
	local label=$1 out
	shift
	out="$(env "$@" go test -count=1 -v -run Mailpit ./mail 2>&1)" || { echo "$out"; echo "mailpit-test: $label FAILED" >&2; fail=1; return; }
	if grep -q -- '--- SKIP' <<<"$out"; then
		echo "$out"; echo "mailpit-test: $label skipped a test" >&2; fail=1; return
	fi
	echo "mailpit-test: $label ok ($(grep -c -- '--- PASS' <<<"$out") tests)"
}
run "STARTTLS with sign-in" GOJIBERRY_TEST_MAILPIT_SMTP="127.0.0.1:$tls_smtp" GOJIBERRY_TEST_MAILPIT_API="http://127.0.0.1:$tls_api" \
	GOJIBERRY_TEST_MAILPIT_CA="$work/cert.pem" GOJIBERRY_TEST_MAILPIT_USER=mailer GOJIBERRY_TEST_MAILPIT_PASS=test-password
run "no TLS on loopback" GOJIBERRY_TEST_MAILPIT_SMTP="127.0.0.1:$plain_smtp" GOJIBERRY_TEST_MAILPIT_API="http://127.0.0.1:$plain_api"
exit $fail
