# Release checklist

Before tagging a release:

1. `make check` passes on `main` (build, vet, tests, lint, the wasm checks, the nested modules, the
   generated files and the API dumps up to date).
2. **`make a11y-test` passes in the OpenDocs repository** (the accessibility gate: axe-core and the keyboard tests in a real
   browser; see [accessibility-testing.md](accessibility-testing.md)).
3. **The manual screen-reader script has been run** on the release build with NVDA, JAWS and
   VoiceOver, the results table is filled in, and no step marked ★ failed (accessibility-testing.md,
   part 2). A release is not made without it.
4. `docs/roadmap.md` is up to date (the entries this release closes are marked done).
5. The known limits that changed are in `docs/limits-register.md`.
