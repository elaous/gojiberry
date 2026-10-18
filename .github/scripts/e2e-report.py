#!/usr/bin/env python3
"""Reads the `go test -json` output of the browser suite and writes a Markdown report on stdout:
the totals, every test that failed with the end of its output, and the tests that started and never
finished (a panic or a timeout ends the run: they did not run to the end). Exit status 0 when every
test passed, 1 otherwise, 2 when there is nothing to read (the run died before the first test).

    e2e-report.py e2e.json [run-url]
"""
import json
import sys
from collections import OrderedDict

TAIL = 14        # lines of a failed test's output kept
MAX_FAILED = 60  # failed tests described in full; the rest are listed by name


def main(path, run_url=""):
    tests = OrderedDict()   # name -> {"out": [], "result": None, "elapsed": 0}
    package_out, package_result = [], None
    try:
        lines = open(path, encoding="utf-8", errors="replace").read().splitlines()
    except OSError:
        lines = []
    for raw in lines:
        try:
            ev = json.loads(raw)
        except ValueError:
            package_out.append(raw)  # build errors and a panic's text come as plain lines
            continue
        name, act = ev.get("Test"), ev.get("Action")
        if name:
            t = tests.setdefault(name, {"out": [], "result": None, "elapsed": 0})
            if act == "output":
                t["out"].append(ev.get("Output", "").rstrip("\n"))
            elif act in ("pass", "fail", "skip"):
                t["result"], t["elapsed"] = act, ev.get("Elapsed", 0)
        else:
            if act == "output":
                package_out.append(ev.get("Output", "").rstrip("\n"))
            elif act in ("pass", "fail"):
                package_result = act
    if not tests and not package_out:
        print("# Nightly browser run: no output\n\nThe run ended before the first test.")
        if run_url:
            print("\n[The run](%s)" % run_url)
        return 2
    passed = [n for n, t in tests.items() if t["result"] == "pass" and "/" not in n]
    failed = [n for n, t in tests.items() if t["result"] == "fail" and "/" not in n]
    skipped = [n for n, t in tests.items() if t["result"] == "skip" and "/" not in n]
    unfinished = [n for n, t in tests.items() if t["result"] is None and "/" not in n]
    print("# Nightly browser run: %s\n" % ("failed" if failed or unfinished or package_result == "fail" else "passed"))
    print("%d passed, %d failed, %d skipped, %d did not finish.\n" % (len(passed), len(failed), len(skipped), len(unfinished)))
    if run_url:
        print("[The run](%s)\n" % run_url)
    if unfinished:
        print("## Did not finish (the run was ended by a panic or a timeout; tests after these did not run)\n")
        for n in unfinished:
            print("- `%s`" % n)
            tail = [l for l in tests[n]["out"] if l.strip() and not l.startswith("=== ")][-TAIL:]
            if tail:
                print("\n  ```\n" + "\n".join("  " + l for l in tail) + "\n  ```\n")
        print()
    for n in failed[:MAX_FAILED]:
        t = tests[n]
        sub = [m for m, u in tests.items() if m.startswith(n + "/") and u["result"] == "fail"]
        print("## `%s` (%.1fs)\n" % (n, t["elapsed"]))
        if sub:
            print("Failed parts: " + ", ".join("`%s`" % s[len(n) + 1:] for s in sub[:10]) + "\n")
        tail = [l for l in t["out"] if l.strip() and not l.startswith("=== ")][-TAIL:]
        print("```\n" + "\n".join(tail) + "\n```\n")
    if len(failed) > MAX_FAILED:
        print("## Also failed\n")
        for n in failed[MAX_FAILED:]:
            print("- `%s`" % n)
        print()
    if package_result == "fail" and not failed and not unfinished:
        print("## The package failed without a failed test\n")
        print("```\n" + "\n".join([l for l in package_out if l.strip()][-30:]) + "\n```\n")
    rest = [l for l in package_out if l.strip()][-30:]
    if rest and (unfinished or package_result == "fail"):
        print("## End of the package output\n")
        print("```\n" + "\n".join(rest) + "\n```")
    return 1 if failed or unfinished or package_result == "fail" else 0


if __name__ == "__main__":
    if len(sys.argv) < 2:
        sys.exit("usage: e2e-report.py e2e.json [run-url]")
    sys.exit(main(sys.argv[1], sys.argv[2] if len(sys.argv) > 2 else ""))
