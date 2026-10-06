---
name: pr-review
description: Review, fix and merge a Gojiberry PR end to end with a fresh background review session, then clean up branches and worktrees. Use when asked to "review and merge", finish a PR, or clean up after one.
---

# PR review and merge

Run this to take a PR from "pushed" to "merged and cleaned up" without leaving loose ends.

## 1. Open the PR

- Branch from the latest `main`; if the branch is old, merge `main` in first and resolve conflicts. If `main` already solved the same problem differently, the branch is obsolete: say so instead of opening a conflicting PR.
- Open it as a draft following the repo's PR conventions, with the checks run locally first (`go test ./...`, `golangci-lint`, wasm vet where it applies).

## 2. Fresh review

- Start a **new background session** (never review in the session that wrote the change) with the PR number and the instruction to review read-only for correctness, concurrency, security (access control, bounds) and test gaps.
- Wait for its findings; do not review your own diff as a substitute.

## 3. Address feedback

- Fix every finding that is correct, with a test where it makes sense; push to the same branch.
- Reply on each thread that is not fixed, saying why. Resolve threads you addressed.
- Re-run CI until green. Repeat the fresh review if the fix was large.
- Leave no open threads, red checks, or TODOs behind.

## 4. Merge

- Only when the review is clean and CI is green: **squash and merge** (the user's standing instruction for "if ok, merge"; without that instruction, wait for the user to say "merge").

## 5. Clean up

- Delete the PR's branch once merged (never `main`).
- Delete other branches that are obsolete: no commit in the last 48 hours **and** nothing unmerged, or already superseded by `main`. List them first with any open PR or unmerged commits marked, and delete only what the user has approved.
- Remove stale worktrees (`git worktree list`, `git worktree remove`, `git worktree prune`) and local branches whose remote is gone.
- Report what was merged, what was deleted, and anything left on purpose.
