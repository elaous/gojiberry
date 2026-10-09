# Git history rewrite (2026-10)

In 2026-10 the git history of this repository was rewritten to remove seven committed binaries. This note says what
changed and what you must do.

1. **What was removed.** `docsh` (54 MB), `collabhub` (50 MB), `examples/gin-ssr-demo/gin-ssr-demo` (21 MB),
   `examples/echo-ssr-demo/echo-ssr-demo` (10 MB), `translateserver` (9.7 MB), `examples/collab-editor-app/server`
   (8.9 MB), and the root `canvas-editor-showcase` binary (3 MB).
2. **Result.** `.git` went from 115 MB to 5 MB. Every commit hash changed. The tree of `main` is identical, because no
   binary was tracked at HEAD. The rewritten `main` starts at `82d6cad`.
3. **Existing clones and worktrees.** Re-clone. For a clean checkout you can instead run
   `git fetch origin && git reset --hard origin/main`. Do not push an old branch back: it would bring the old history
   with it.
4. **Branches and pull requests.** Old branches were deleted. PR numbers and titles are unchanged, but GitHub still
   shows the old commits through `refs/pull/*`. A fresh clone does not fetch those refs; only GitHub support can purge
   them.
5. **Do not commit build artifacts again.** Build to a directory outside the repository, or to a path that
   `.gitignore` covers (it now lists the seven binaries). Check with `git status` before committing.
6. **Backup.** The pre-rewrite bundle exists only in the maintainer's hands.

See also [the roadmap](roadmap.md) backlog item 26.
