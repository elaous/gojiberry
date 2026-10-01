package main

import "github.com/elaous/gojiberry/canvas"

// State is everything the canvas tree depends on.
type State struct {
	Mode  canvas.Mode
	Pages int
	// Source is the document's starting Markdown; once the text is edited
	// on the canvas the canvas owns it, until Source changes.
	Source string
	Image  canvas.ImageSource
	Note   string
}

const sample = "# Gojiberry editor\n\n" +
	"Everything on this canvas is a **gox** tree: pages, a text flow, a picture, a floating note and a table. " +
	"Switch to *edit* mode and click into the text to type; select text and use Ctrl+B, Ctrl+I, Ctrl+U or the style editor above.\n\n" +
	"Registered features: multi-cursor (Ctrl+D picks the next match, Ctrl+click adds a cursor), line operations " +
	"(Alt+Up/Down move, Alt+Shift+Up/Down copy, Ctrl+Shift+K delete) and Markdown copy and paste (Ctrl+Shift+C / V). " +
	"Press Mod+Enter for a page break.\n\n" +
	"## Second thoughts\n\n" +
	"The text flows across the pages, around the picture and the table. Edit this paragraph, then add some more text so that it " +
	"runs on to the next sheet. Edit this paragraph, then add some more text so that it runs on to the next sheet. " +
	"Edit this paragraph, then add some more text so that it runs on to the next sheet.\n\n" +
	"H~2~O and E=mc^2^, ~~struck~~, ++underlined++ and ==highlighted== text, `code` too. " +
	"Hover the link to [Gojiberry on GitHub](https://github.com/elaous/gojiberry) for its address.\n\n" +
	"- bullets and\n- numbered lists work: try Ctrl+Shift+8 and Ctrl+Shift+7\n- [ ] check boxes too (click the box in edit mode)"

const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="240" height="160">
<defs><linearGradient id="g" x1="0" y1="0" x2="1" y2="1">
<stop offset="0" stop-color="#ff7a59"/><stop offset="1" stop-color="#3b2bd9"/></linearGradient></defs>
<rect width="240" height="160" fill="url(#g)"/><circle cx="80" cy="60" r="34" fill="#ffd23f"/>
<path d="M0 160 L90 90 L150 140 L190 110 L240 160 Z" fill="#1b1b3a"/></svg>`
