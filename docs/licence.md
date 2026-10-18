# Licence and credits

**Decided (owner, 2026-10-18): route B.** The project is under the **GNU Affero General Public License, version 3** (AGPL-3.0,
the file [LICENSE](../LICENSE)), with a separate **commercial licence** by agreement ([COMMERCIAL.md](../COMMERCIAL.md)), and
asks for **credit** to the project and its repository. It is open source: anyone may use it commercially under the AGPL if
they publish their own source. The docs do not call it "non-commercial", because it is not.

## The pieces

| File | What it is |
| --- | --- |
| `LICENSE` | The AGPL-3.0 text, unchanged (from the SPDX list). |
| `NOTICE` | Copyright line, the pointer to the licence and the commercial licence, and the **additional terms under section 7(b)** of the AGPL: keep the notices and the credit "Made with the Gojiberry project, github.com/elaous/gojiberry". |
| `COMMERCIAL.md` | What the commercial licence is for, what it covers, how to ask. A description, not an offer. |
| `CONTRIBUTING.md` | The checks, and the terms on which a contribution is accepted: AGPL for everyone plus the owner's right to relicense, so that the commercial licence can exist. |
| About tab (showcase) | The credit, a link to the source (section 13 of the AGPL asks a network service to offer its source to its users), to the licence, the notice and the commercial licence. Help > About Gojiberry opens it. |

## What is not settled

- **A lawyer has not read any of this.** The section 7(b) wording, the contributor terms and the commercial licence
  description are drafts; read them before the project relies on them. Additional terms under the AGPL are limited to the
  kinds section 7 lists (preserving notices and attributions is one); a term that restricts more would be removed by anyone
  who receives the code.
- **Who holds the copyright.** The notice says "the Gojiberry authors". If the owner wants a name or a company there, change
  `NOTICE` and the commercial licence; relicensing contributions depends on the contributor terms above.
- **A contact address** for commercial licences (COMMERCIAL.md uses an issue until there is one).
- **Source file headers.** The files do not carry a licence header each; the root `LICENSE` and `NOTICE` apply. SPDX headers
  can be added by a script if wanted.

## Dependencies

A scan on 2026-10-18 of the Go modules the repository's packages build with found Apache-2.0, MIT, BSD and ISC licences and
one MPL-2.0 module (`github.com/hashicorp/hcl/v2`, used by the tooling), which may be combined with the AGPL. No GPL, LGPL or
AGPL code and none under a licence that forbids the combination. Run the scan again when a dependency is added.

## Material that has its own licence

These are carried or downloaded by the project and keep the licence their authors chose; the AGPL does not relicense them.

- **Fonts**: the repository carries a small font data file only; the fonts the page loads come from their publishers (many
  Google Fonts under the SIL Open Font License 1.1 or Apache-2.0) and keep their licences. The project does not record a licence per font in code; check a
  font's licence before you bundle it in a product.
- **Dictionaries**: the English Hunspell dictionary in `spell/dict` is the SCOWL word list under its own permissive licence,
  stated in [spell/dict/NOTICE](../spell/dict/NOTICE); dictionaries for other languages are fetched and have their own.
- **Voices** (Piper voices, the reader hub): each voice has the terms its publisher wrote; the page shows them before a
  download ([reader-voices.md](reader-voices.md)).
- **Map data and tiles**: © OpenStreetMap contributors (ODbL), shown on every map ([osmmap](../osmmap)).
- **Icons** (Material Symbols): Apache-2.0.
- **Test tools** such as axe-core (`internal/e2etest/axe`): their own licence file is beside them.

A repository split ([repo-split.md](repo-split.md)) copies `LICENSE`, `NOTICE` and `COMMERCIAL.md` into each new repository,
or gives it a more permissive licence of its own if it is meant to be used as a library (a decision per repository).
