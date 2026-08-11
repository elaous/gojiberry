# Fofoca

_Fofoca (Portuguese slang): gossip, the hallway chatter -- the stuff
that's true and worth knowing but doesn't belong in a formal doc._

This is that, for gojiberry. `docs/*.txt` is generated API reference,
`README.md` is the deliberate, curated intro/reference -- this file is
neither: a place for a quirk you tripped over, something true today
that might not be tomorrow, a half-formed idea nobody's committed to.
Low ceremony, no obligation to stay accurate forever -- prune anything
stale or promote it into `README.md`/a real issue once it's settled.

_(This file's own existence is a best-effort interpretation of a
request to "create a fofoca" for this repo -- if that's not what was
meant, say so and it's easy to redirect.)_

Newest first.

- **opendocs is gojiberry's own biggest real-world stress test.**
  Several gojiberry-side findings this file (and gojiberry's own
  issue history) accumulates started life as "opendocs hit this" --
  `domrt/browser`'s `js.Global()`/`syscall/js` surface in particular
  gets exercised more thoroughly there than by gojiberry's own test
  suite alone. Worth checking opendocs' own `docs/FOFOCA.md` and
  `docs/POSTMORTEMS.md` when debugging something in `domrt` that
  smells cross-cutting.

- **`// Code generated ... DO NOT EDIT.` headers opt a file out of
  golangci-lint by default.** Not gojiberry-specific, but easy to
  forget when a `gox`-preprocessed file's own generated `.go` output
  ends up carrying real logic beyond what the preprocessor itself
  emits -- lint issues in that logic go quiet rather than loud. Keep
  hand-written logic in a plain sibling file, not folded into
  generated output, so it stays visible to tooling.
