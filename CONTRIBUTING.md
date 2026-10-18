# Contributing

Thank you for helping. Before you send a change:

- Run the checks the repository runs: `make lint wasm-lint fmt-check docs-check generate-check` and the tests of the
  packages you touched (`make check` runs everything).
- Update the docs that describe what you changed (`docs/status.md`, `docs/features.md`, `docs/feature-testing.md`) and the
  six language packs for any message the page shows.
- An experimental feature goes behind a build flag (`docs/phase-2.md`).

## Licence of contributions

The project is under the GNU AGPL, version 3, and also offers a commercial licence ([COMMERCIAL.md](COMMERCIAL.md)). To be
able to do both, a contribution is accepted on these terms (a short contributor agreement; a lawyer should review the final
wording):

1. You wrote the contribution, or you have the right to give it, and nobody else's rights or employer's terms stand in the
   way.
2. You license it to everyone under the GNU AGPL, version 3, like the rest of the project.
3. You also grant the project's owner a perpetual, worldwide, non-exclusive, royalty-free licence to use, change and
   sublicense your contribution under other terms, including the commercial licence, and to relicense it under another free
   software licence if the project moves to one.
4. You keep the copyright in your contribution.

Sending a pull request says you agree. If you cannot agree, say so in the pull request and we will talk about it before
anything is merged.
