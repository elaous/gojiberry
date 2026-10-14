# Translating the interface

Messages are asked for by id with their English text: `T("check.run", "Check this document")` in an
application, `tr("check.run", "...")` in `components`. English is the text in the code. The other
languages are the packs in [`i18n/packs`](../i18n/packs): `de.json`, `es.json`, `fr.json`, each a map from id to text.

## Adding a message

1. Write the call with the English text. Keep the `%s`, `%d` verbs, and give the person a full sentence
   rather than fragments that are glued together (word order differs).
2. Add the id to every pack. `go test ./i18n` fails with the id and the file that asks for it if a pack
   lacks it (`TestEveryMessageInTheSourcesIsInThePacks`), and if one pack has an id another lacks or a
   different number of verbs (`TestShippedPacksAreComplete`).
3. A pack id that no source asks for any more fails `TestNoStaleMessagesInThePacks`: delete it from the packs
   when you remove the feature.
4. Ids built at run time (`"tab."+name`, `"check.sev."+severity`) are not seen by that test: add them to the
   packs by hand, next to the code that builds them.

## Messages that depend on a number

Do not glue "1 document" and "2 documents" from fragments. Ask for the plural form with `Tn` (the showcase) or
`trn` (components): `Tn("doc.count", "%d document", "%d documents", n)`. The packs hold one text per plural
category of the language, under the id with `#category` added: `"doc.count#one"` and `"doc.count#other"` for
English, German, Spanish and French; Russian needs `#one`, `#few` and `#many`; Arabic has six categories;
Japanese only `#other`. The category rules are in `i18n.PluralCategory` (CLDR cardinal rules for whole
numbers). The shipped packs must have `#other` for every plural id (the test checks it); a language that lacks
a category falls back to `#other`, then to the English in the code. Each text takes the number as its `%d`.

Numbers and dates: `i18n.Number(lang, n, decimals)`, `i18n.Percent`, `i18n.Date(lang, t, style)` and
`i18n.Time` write them the way the language does (separators, month names, order); the number in a plural
message is written that way too. `Catalog.Ago(lang, d)` says how long ago something was ("5 minutes ago").

## Adding a language

Copy `es.json`, change the top-level key to the language tag (`"pt"`, `"pt-BR"`), translate the values and
keep the verbs in the same number (order may change where the language needs it only if you use indexed verbs
such as `%[2]s`). Add the language's own name to `languageNames` in the showcase's `extras.go`. A right-to-left
language needs `i18n.Direction` to know it (it does for Arabic, Hebrew, Persian, Urdu and a few more) and a
check of the page in that direction.

## Languages that are fetched when chosen

Portuguese, Arabic and Hebrew are not in the program. Their packs are files next to the page,
`examples/canvas-editor-showcase/static/lang-<code>.json` (the same shape as the embedded packs: one
top-level key, the language), copied beside `index.html` by `make showcase-dist` and by the e2e harness, and fetched
the first time the language is asked for (`?lang=ar`, the Language menu, the preferences). A visitor who never
chooses them downloads nothing, and the wasm does not grow with each language. `TestLazyPacksAreComplete` holds them to
the same standard as the shipped packs (every message, the same verbs in the same order, a text for every plural
category the language uses: Arabic has six, Hebrew three).

To add one: write `static/lang-<code>.json`, add the language's own name to `languageNames` and `lazyLanguages`
in the showcase (`extras.go`, `langload.go`). **These three were drafted by the model that wrote this code and have
not been read by a native speaker:** ask one to review them before they are relied on (Portuguese is written
in the European form). Right-to-left languages turn the page around (`dir="rtl"`); `TestLazyLanguagesAndRightToLeft`
and `TestRightToLeftInterfacePassesAxe` check that the page follows and that axe finds nothing in Arabic. What a
reader of those languages would still look at by eye: the canvas editor's own layout of mixed-direction text
(item 64), and wide German-like strings do not apply but long Arabic ones can wrap differently in the narrow side panel.

## Plain words (`en-x-plain`)

"English, plain words" (`static/lang-en-x-plain.json`, chosen in the language menu or the preferences) is a variant of
English for people who find the usual wording hard (roadmap 65): short sentences, everyday words, and the same
meaning and the same warnings. It rewrites only the messages that are hard to read, those about signing in, keys,
encryption and share links, and everything else falls back to English; `TestLazyPacksAreComplete` therefore only
requires that each message it holds exists and keeps its verbs. The page language is `en-x-plain` (a valid private-use
tag), so a screen reader keeps reading it as English. **It was written by the model that wrote this code, not by a plain
language editor or by readers who need it:** have it reviewed (and extended to the rest of the page) before it is relied on.

## What is not done

Plural rules for fractions are in
[roadmap item 83](roadmap.md). The messages written before plural forms existed still use neutral wording.
