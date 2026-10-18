# Testing guide: each feature, by hand and by test

For every feature: the steps to try it, what you should see, and the automated test that covers it (all in `examples/canvas-editor-showcase` unless a path is given; run one with `GOJIBERRY_REQUIRE_BROWSER=1 go test ./examples/canvas-editor-showcase -run <TestName>`). Build the showcase with `make showcase-dist` and serve `dist/showcase` with any static file server (for example `python3 -m http.server -d dist/showcase`), then open it in a browser (see [testing-workflow.md](testing-workflow.md) for the whole workflow and [accessibility-testing.md](accessibility-testing.md) for the screen-reader script). A picture of each feature is in [features.md](features.md).

Every feature also has these checks: it works with the keyboard alone, nothing is announced twice, the page shows no error in the console, and the Check tab and axe find nothing new.

## Editing on the canvas

**Try it.** Click **Mode** to edit, click in the text and type. Press Ctrl+D on a word twice, type, and check both places change. Press Ctrl+Z.

**You should see.** What the picture in [features.md](features.md#editing-on-the-canvas) shows.

**Automated.** e2e_test.go (`TestEndToEnd`), e2e_typing_test.go, e2e_mirror_test.go

## Format

**Try it.** Select a word, press **B**, then change its colour. Press Enter in a bullet list and check it continues.

**You should see.** What the picture in [features.md](features.md#format) shows.

**Automated.** e2e_test.go, e2e_toolbar_test.go, e2e_icons_test.go

## Styles

**Try it.** Make a style from a selection, apply it to two paragraphs, then edit the style and watch both change.

**You should see.** What the picture in [features.md](features.md#styles) shows.

**Automated.** e2e_theme_test.go, e2e_configsync_test.go

## Symbols

**Try it.** Open the tab, search for "arrow" and press Enter on one.

**You should see.** What the picture in [features.md](features.md#symbols) shows.

**Automated.** e2e_gridkeys_test.go, e2e_keyboard_test.go

## Keys

**Try it.** Rebind a command to a free key, reload the page and use it.

**You should see.** What the picture in [features.md](features.md#keys) shows.

**Automated.** e2e_keyboard_test.go, e2e_prefs_test.go

## Comments

**Try it.** Select a word, press Ctrl+Alt+N, type a note, then reply and resolve it from the list.

**You should see.** What the picture in [features.md](features.md#comments) shows.

**Automated.** e2e_mentions_test.go, e2e_roundtrip_test.go

## Review (suggestions)

**Try it.** Tick **Suggest changes**, delete a word, type another, then accept one and reject the other.

**You should see.** What the picture in [features.md](features.md#review-suggestions) shows.

**Automated.** e2e_review_test.go, e2e_live_test.go

## Outline

**Try it.** Fold a heading with the arrow, then press **Move section down** on another and read the document order.

**You should see.** What the picture in [features.md](features.md#outline) shows.

**Automated.** e2e_test.go (`TestEndToEnd`)

## Footnotes

**Try it.** Press Ctrl+Alt+F, type a note, save, scroll to the foot of the page. Add a very long note and see it continue on the next page.

**You should see.** What the picture in [features.md](features.md#footnotes) shows.

**Automated.** e2e_test.go, e2e_roundtrip_test.go, e2e_mirror_caret_test.go

## Columns

**Try it.** Edit > Columns > Two columns, drag the grip, then choose Reset column widths.

**You should see.** What the picture in [features.md](features.md#columns) shows.

**Automated.** e2e_columns_test.go

## Table styles

**Try it.** In the table demo (`examples/canvas-table-demo`) or a table in the editor, switch to edit mode, press the table's corner handle and choose **Table style: Blue header, banded**; open the menu again and check the style is marked, then change one cell's background by hand and check the mark goes.

**You should see.** What the picture in [features.md](features.md#table-styles) shows.

**Also try.** Type three lines of a Markdown table and press Enter, open the Styles tab, choose a style, then define one of your own (a name and colours like `#c2410c`) and choose it; export as JSON and look for `table.schemes`. Add a row to a dressed table in the table demo: the bands follow.

**Automated.** `TestADressedTableStaysDressedWhenRowsAndColumnsChange`, `TestStylesThePersonDefinesJoinTheBuiltInOnes` (canvas), `TestTableStylesAreKeptWithTheDocument` (docfile), `TestTableStylesPickerDefineAndFollowRows` (showcase), `TestTableStylesDressAWholeTableAndAreRecognised` and `TestTableMenuOffersTheStylesAndChecksTheCurrentOne` (canvas), `TestEndToEnd` in examples/canvas-table-demo

## Image legend and picture formats

**Try it.** Insert > Picture, pick a WebP or PNG file, tick **Add a legend**, type a text, insert; export to Word and Markdown.

**You should see.** The legend in italics under the picture, in the exports too.

**Also try.** Insert an `.svg` and an `.avif` file; export to Word and check both are there as pictures.

**Automated.** `TestAPictureWithALegendAndAWebPPicture`, `TestSVGAndAVIFPicturesAreStoredAsPNG` (examples/canvas-editor-showcase), `export/images_test.go`, `canvas/image_test.go`

## Find a picture online

**Try it.** Pictures tab > Find a picture online: the Search button is off; turn on Openverse, search "harbour", read the credit under a result, press **Insert with its credit**.

**You should see.** The picture at the caret with a legend like "Harbour by Ana, CC BY 4.0, via Openverse", also in the HTML export; in Word the picture's Alt Text title holds the credit.

**Also try.** Search, then press **More results**; put an `image-repos.json` next to the page and check the repository is listed, off.

**Automated.** `TestImageCreditTravelsInWordAndODTProperties` (export), `TestOperatorListOfRepositoriesAndMoreResults`, `TestFindAPictureInARepositoryAndInsertItWithItsCredit` (examples/canvas-editor-showcase), package `imagerepo` tests

## Mentions

**Try it.** Open the same shared document as two people (for example `?name=Ana` and `?name=Bo`); as Ana comment with "@Bo can you check this?"; look at Bo's Comments tab.

**You should see.** "Mentions of you" with Ana's comment, announced once; Ana sees none; "Mark as read" removes it, and it stays gone after the page is reloaded.

**Automated.** `TestAMentionedPersonIsToldAndCanMarkItRead` (examples/canvas-editor-showcase), `TestMentionsOfListsWhatOthersWroteAboutThePerson` (canvas), package `mentionmail` (mail once, retry a failure, cap a mail)

**Push when the page is closed.** *Try it:* serve a push server (docs/web-push.md) and `push.json`, open the Comments tab, tick "Notify me of mentions when this page is closed", allow notifications, close the page, and have someone mention you. *You should see:* a notification naming who and the document; clicking it opens the page; unticking stops it (this last part with a real push service is not covered by an automated test). *Automated:* `TestMentionNotificationsWhenThePageIsClosed` (stand-ins for the push API), package `webpush` (encryption checked by an independent decryptor, VAPID, SSRF refusals, handler), package `mentionpush`, `wasmserve/sw-push.test.js` (`make sw-test`).

## Maps

**Try it.** Build with `GOFLAGS=-tags=exp_map` (or `experimental`), open the Pictures tab in edit mode, type `8FVC9G8F+6W` (or an address, or `48.8584, 2.2945`), pick a zoom and press *Insert the map*.

**You should see.** A map picture at the caret with a red marker at the centre, *Map data © OpenStreetMap contributors (ODbL)* under it, and the alt text *Map of ...* in the picture's list. A latitude over 90 is refused in words.

**Automated.** `TestInsertAMapFromAPlusCodeAndFromAnAddress`, `TestMapsAreNotInADefaultBuild` (examples/canvas-editor-showcase), package `osmmap` tests (Plus Code vectors of the specification, round trip, coordinates, Nominatim and tile stand-ins, rendering)

## Text formats as they are

**Try it.** Build with `GOFLAGS=-tags=exp_textformats` (or `experimental`), then on the Files tab open a `.csv`, a `.yaml` and a `.json` file that is data; or `opendoc pack out.opendoc folder` with such files in the folder.

**You should see.** The CSV as a table with its first row as the header, the YAML and the JSON (indented) as code blocks; a file with binary content is left out with its reason.

**Automated.** package `textimport` tests, `transfer/textformats_test.go`, `TestPackTakesTextFormats` (cmd/opendoc); `make test-experimental`

## Table grips and sizes

**Try it.** In edit mode type three lines (`| Item | Qty |`, `|---|---|`, `| Pens | 1 |`) and Enter; drag the edge between the two column grips to the right, and the edge under the first row grip down.

**You should see.** The column and the row grow as you drag, and stay that way after the document is saved and opened again. Outside edit mode nothing is resized.

**Automated.** `TestDraggingATableGripResizesAColumnAndARow` (examples/canvas-editor-showcase), canvas `table_sizes_test.go`, `TestTableSizesRoundTrip` (docfile)

## Table formulas

**Try it.** In the table demo or a table in the editor, edit a cell and type `=6*7`; type `=SUM(B2:B3)` in another; make two cells refer to each other; export to Word.

**You should see.** 42, the sum, #CYCLE! in both of the circular cells, and the values (not the formulas) in the Word file.

**Also try.** Hold the pointer over a blue calculated cell (the formula shows); click it and type (nothing changes); press the corner switch (formulas show in red and can be edited). Type `\=1+1` in a cell (it shows `=1+1`); open the table menu and choose **Show formulas** (the formulas show, choose it again for the values); look at the grips: A, B, C above and 1, 2, 3 beside.

**Automated.** `TestFormulaViewOpensOnCalculatedAndFormulaCellsAreReadOnlyThere`, `TestFormulaTooltipAndColours`, `TestEscapedEqualsIsPlainText`, `TestColumnNames`, `TestShowFormulasSwitchesWhatIsDrawn`, `TestTableFromMarkdownKeepsFormulasAndAlignment`, `TestMarkdownExportKeepsTheFormulaSoItWorksWhenReadBack` (canvas), `TestTableFormulas`, `TestFormulaCycleAndEditing`, `TestFormulaCellsShowTheirValue` (canvas), `TestTableFormulasAreWrittenAsTheirValues` (export), `TestEndToEnd` in examples/canvas-table-demo

## Equations

**Try it.** Pictures tab > Equations: write `x = frac{-b + sqrt{b^2 - 4ac}}{2a}`, a text alternative in words, tick **In the line of text**, and insert it in the middle of a sentence. Also try `sum_{i=1}^{n} i` and `matrix{a & b \\ c & d}` as blocks, and type `$x^2$` in the text.

**You should see.** What the picture in [features.md](features.md#equations) shows; the Word, HTML and text exports have the words of the alternative in the sentence.

**Automated.** `TestTypesetFractionHasARuleAndPutsTheNumeratorAbove`, `TestTypesetScriptsAndRoots`, `TestInlineEquationSitsOnTheBaselineOfItsLine`, `TestBlockEquationIsTypesetNotTyped` (canvas), `TestTypesetLimitsOfASumGoAboveAndBelowInABlock`, `TestTypesetBracketsGrowWithAFraction`, `TestTypesetMatrixAndAlignedEquations`, `TestTypingDollarsMakesAnInlineFormula` (canvas), `TestTypesetterAndMathMLReadTheSameSymbols`, `TestMatrixAndAlignedEquationsInMathMLAndOfficeMath`, `TestInlineFormulaIsInlineMathMLInHTML`, `TestInlineFormulaIsOfficeMathInTheLineInWord`, `TestOfficeMathMatricesAndAlignedEquationsReadBack` (export), `TestAnInlineEquationStaysInItsLineAsText` (docfile), `TestAnInlineEquationStandsInItsSentence` (examples/canvas-editor-showcase)

## Markdown tables

**Try it.** In edit mode type `| Qty | Total |`, Enter, `|---|---|`, Enter, `| 3 | =A2*2 |`, Enter. Then press the corner switch at the table's top right.

**You should see.** A table with the header and 6 in the last cell; the switch shows f(x) and the cell shows `=A2*2`; pressing it again shows the eye and 6. A row with fewer cells, or no row of dashes, stays text.

**Also try.** Import a `.md` file with `A claim[^1]` and `[^1]: The note.` (it comes in as a footnote) and a picture at an `https://` address (the Files tab says it was left out). In a table, put the caret in a cell and paste a Markdown table or cells copied from a spreadsheet.

**Automated.** `TestMarkdownFootnotesBecomeFootnotes` (transfer), `TestMarkdownImportFidelityNamesWhatIsLeftOut` (export), `TestPasteIntoATableFillsCellsFromTheActiveOne` (canvas), `TestMarkdownImportSaysWhatItLeftOutAndKeepsItsFootnotes` (showcase), `TestEnterAfterThreeMarkdownTableLinesMakesATable`, `TestFormulaSwitchInTheTopRightCorner` (canvas), `TestMarkdownImportTitleImagesAndFormulas` (transfer), `TestTypingAMarkdownTableMakesATable` (examples/canvas-editor-showcase)

## References

**Try it.** Edit mode, References tab: type two authors (one per line, `Silva, Ana Maria` and `Costa, Bruno`), a title, a publisher and a year; Add the source; press **Cite it here**; change the style to MLA; press **Insert the list of references**.

**You should see.** `(Silva & Costa, 2020)` in the text, the entry rewritten in MLA, and the list at the caret with the title in italics.

**Automated.** `TestCiteASourceAndInsertTheListOfReferences` (examples/canvas-editor-showcase), package `reference` tests, `TestSourcesAreKeptWithTheDocument` (docfile)

## Two-page spread

**Try it.** View > Two-page spread; press the folded corner at the bottom right of the right-hand page.

**You should see.** What the picture in [features.md](features.md#two-page-spread) shows.

**Automated.** e2e_pagesetup_test.go, canvas unit tests (`TestSpreadDrawsTheFoldShadeAndTurnTabs`)

## Page and print layout

**Try it.** Choose Letter, landscape, type a header with `{title}` and check it on every page.

**You should see.** What the picture in [features.md](features.md#page-and-print-layout) shows.

**Automated.** e2e_pagesetup_test.go, e2e_printlayout_test.go

## Pictures

**Try it.** Insert a picture, leave the description empty and open the Check tab; then describe it and check again.

**You should see.** What the picture in [features.md](features.md#pictures) shows.

**Automated.** e2e_images_test.go, e2e_elements_test.go

## Find and replace

**Try it.** Search for a word that appears twice, press Enter to step, then Replace all.

**You should see.** What the picture in [features.md](features.md#find-and-replace) shows.

**Automated.** e2e_find_test.go, e2e_searchall_test.go

## Spelling

**Try it.** Type `teh`, right-click or use the list to correct it; choose another dictionary language.

**You should see.** What the picture in [features.md](features.md#spelling) shows.

**Automated.** e2e_spell_test.go, e2e_dictionary_test.go

## Language

**Try it.** Select a sentence, set its language to Arabic, and check the paragraph direction and the reading copy.

**You should see.** What the picture in [features.md](features.md#language) shows.

**Automated.** e2e_lang_test.go, e2e_bidi_test.go, e2e_ime_test.go

## Stats

**Try it.** Select a paragraph and compare the Selection column to the Document column.

**You should see.** What the picture in [features.md](features.md#stats) shows.

**Automated.** e2e_test.go (`TestEndToEnd`)

## Share

**Try it.** Select a sentence, copy the link, open it in another tab.

**You should see.** What the picture in [features.md](features.md#share) shows.

**Automated.** e2e_share_test.go, e2e_links_test.go

## People

**Try it.** Add a person as a viewer and check they cannot edit; remove them.

**You should see.** What the picture in [features.md](features.md#people) shows.

**Automated.** e2e_people_test.go, e2e_peopleserver_test.go, e2e_encrypt_keys_test.go

## Accessibility check

**Try it.** Insert a table with an empty header row and press **Check this document**.

**You should see.** What the picture in [features.md](features.md#accessibility-check) shows.

**Automated.** e2e_a11y_test.go, e2e_a11y_rules_test.go, e2e_axe_test.go

## Files: import and export

**Try it.** Choose PDF and read what is left out; export it and open the file.

**You should see.** What the picture in [features.md](features.md#files-import-and-export) shows.

**Automated.** e2e_fidelity_test.go, e2e_exporthtml_test.go, e2e_pack_test.go

## Save

**Try it.** Save, edit, save again, then open the older version from the list.

**You should see.** What the picture in [features.md](features.md#save) shows.

**Automated.** e2e_lifecycle_test.go, e2e_folders_test.go, e2e_encrypt_test.go

## Migrate

**Try it.** Bring a Word file with a comment and open it.

**You should see.** What the picture in [features.md](features.md#migrate) shows.

**Automated.** e2e_migrate_test.go

## Console

**Try it.** Type `ls`, then `cat` a document's name.

**You should see.** What the picture in [features.md](features.md#console) shows.

**Automated.** e2e_console_test.go

## Public link

**Try it.** Publish a copy, open the read-only link, and watch the countdown.

**You should see.** What the picture in [features.md](features.md#public-link) shows.

**Automated.** e2e_public_test.go

## Speech

**Try it.** Record one phrase, then throw the take away.

**You should see.** What the picture in [features.md](features.md#speech) shows.

**Automated.** e2e_speech_test.go, e2e_speechkeep_test.go

## Privacy

**Try it.** Turn the counts on, use the editor, and read the local log.

**You should see.** What the picture in [features.md](features.md#privacy) shows.

**Automated.** e2e_privacy_test.go, e2e_privacy_log_test.go, e2e_crash_test.go

## Form fields

**Try it.** Edit mode, Forms tab: press **Text**, give it the label "Order number" and the pattern `[A-Z]{2}\d{3}`, press Done; press **Fill in** and type `AB1234` (the problem is said), then `AB123`; add a **Checkbox** and press its box in the text; press the eye and the pen on a field in the text; press **Lock** (the page says a key is needed: press **Make a key**, then Lock again: the pen goes and the field says which key locked it), **Unlock**, and **Clear form fields**. Insert > Form fields has the same kinds.

**You should see.** The fields drawn in the text with their label and value; a red outline while the value is wrong; the values in the Markdown export as `Order number: AB123`.

**Build tags.** Form fields are experimental: build with `-tags experimental` (or `exp_forms`; add `exp_forms_submit` for the web form with an address) to try them; a default build has no Forms tab. **Automated.** `TestFormsAreNotInADefaultBuild`, `TestAFormFieldInATableCell` (table cells; canvas `table_fields_test.go`), `TestFormFieldsAddFillLockAndClear` (it also downloads the answers; examples/canvas-editor-showcase), `TestFormFieldsAreHTMLControlsAndWordsElsewhere` and `TestHTMLSubmitToWrapsFormFieldsInAForm` (export; the web form also downloaded in the e2e), `TestHTMLFormFieldsReadBackFromTheirPage` (export and htmlimport), package `formdata`, package `formseal`, `TestFormFieldProblems`, `TestFormFieldEyePenAndCheckboxPresses`, `TestClearFormFieldsAndWords` (canvas), `TestFormFieldRoundTripsThroughTheFlow` (docfile), the form-field pair of `TestFidelityMatrixMatchesTheExporters` (export)

## Add-ons

**Try it.** On a page with a whisper add-on: Add-ons tab > Show the models > Download; Speech tab > tick "Dictate on this device", then Dictate, say something and press stop; remove the model again. Without an add-on the tab says "Not on this page".

**You should see.** The model with its size and a link to its terms; progress while it downloads; the text in the document, with nothing sent to any address.

**Automated.** `TestAddonsTabAndDictationOnTheDevice`, `TestDraftDescriptionFromTheDescribeAddon`, `TestReadTheTextInAPictureWithTheOcrAddon`, `TestTranslateTheSelectionWithTheTranslateAddon`, `TestLanguageTabWithTheLlmAddon`, `TestSpellingWithTheSpellAddonsDictionary`, `TestDrawADiagramWithTheDiagramAddon` (examples/canvas-editor-showcase, with stand-in add-ons), packages `addons` (`ParseModels`) and `helper` (prompts, cleaning an answer).

## Preferences

**Try it.** Choose **Larger text** and **High contrast**, reload, and check they stay.

**You should see.** What the picture in [features.md](features.md#preferences) shows.

**Automated.** e2e_prefs_test.go, e2e_prefspanel_test.go, e2e_plainlang_test.go, e2e_access_test.go

## Read aloud

**Try it.** Read aloud > Read the document aloud, pause it, then choose a British voice and read again.

**You should see.** What the picture in [features.md](features.md#read-aloud) shows.

**Automated.** e2e_readmarks_test.go

**Try it (downloaded voices).** Reader voices tab: press "Show the voices to download" (the page says which host it asks), read the size and the link to the terms, press Download, then Reader > Downloaded voices (Piper) and Read the document aloud; remove the voice again. This needs a page with a Piper runtime (docs/reader-voices.md).

**Try it (voices on a server).** Needs a `reader-voices.json` with `"server"` and a running `cmd/voiceserver`: allow voices that use the internet (Read aloud menu), choose Reader > Voices on a server, read the document; the bar says the voice uses the internet. **Automated.** `TestReadWithVoicesOnAServer` (a stand-in for the server), packages `voice/read/hosted` and `voice/server`; the reference Piper runtime: `make piper-runtime-test` (examples/piper-runtime/README.md says what it checks with the real models).

**You should see.** The progress while it downloads; the bar saying "Downloaded voice, running in this page"; the sentence and the word marked while it reads.

**Automated.** `TestDownloadAReaderVoiceAndReadWithIt` (examples/canvas-editor-showcase, with a stand-in network, runtime and Web Audio), packages `voice/read` (`Player`) and `voice/read/piper` (catalogue, config, install with checksum, speaking, cancel). A real runtime has not been run against it.

## Typewriter scrolling

**Try it.** Preferences tab: search "typewriter" and tick it. Click in the text, press the down arrow twenty times and check the caret stays in the middle of the view while the page moves; type on a line and check the page stays; click in another place and check the page does not jump.

**You should see.** What the two pictures in [features.md](features.md#typewriter-scrolling) show.

**Also try.** Tick **Typewriter carriage** too, type a long line (the page slides left under the caret), then press Enter (the page slides back right, past its left edge, and up a line).

**Automated.** `TestTypewriterScrolling`, `TestTypewriterCarriageSlidesThePageSideways` (examples/canvas-editor-showcase), `TestTypewriterKeepsTheLineAtOneHeight`, `TestTypewriterCarriageKeepsTheLetterInOnePlace` (canvas)

## Scripts

(Get and set, below the first test: tick "May read the text of the document" and "May change the document", then run `await editor.setMarkdown("# Heading\n\nSome **bold** words.\n"); await editor.setSelection(0, 7); await editor.setFormat({italic: true, color: "#c01c28"}); await editor.setStyle("Heading 2"); editor.log(JSON.stringify(await editor.format()))` in edit mode; the first line of the document becomes an italic red heading and the log shows the format. `TestScriptsGetAndSetTheDocument` and package `script` `getset_test.go` cover it.)

**Try it.** Scripts tab: press Run (the sample lists the commands and makes the selection bold); replace the script with `try { await editor.text() } catch (e) { editor.log(e.message) }` and run it (refused); tick "May read the text of the document" (a warning appears) and run `editor.log((await editor.text()).length)`; run `editor.log(typeof fetch)` (undefined); run `await new Promise(() => {})` and press Stop.

**You should see.** Each result in the log under the script; reading refused until allowed; no network in the sandbox; Stop ends a script that never finishes.

**Automated.** `TestScriptsRunInASandboxAndAskForWhatTheyMayHave` (examples/canvas-editor-showcase), package `script`.

## Menus and commands

**Try it.** Open Edit with the keyboard (Alt, then arrows) and run a command with Enter.

**You should see.** What the picture in [features.md](features.md#menus-and-commands) shows.

**Automated.** e2e_keyboard_test.go, e2e_test.go
