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
message is written that way too.

## Adding a language

Copy `es.json`, change the top-level key to the language tag (`"pt"`, `"pt-BR"`), translate the values and
keep the verbs in the same number (order may change where the language needs it only if you use indexed verbs
such as `%[2]s`). Add the language's own name to `languageNames` in the showcase's `extras.go`. A right-to-left
language needs `i18n.Direction` to know it (it does for Arabic, Hebrew, Persian, Urdu and a few more) and a
check of the page in that direction.

## What is not done

Relative times, time zones and plural rules for fractions are in
[roadmap item 83](roadmap.md). The messages written before plural forms existed still use neutral wording.
