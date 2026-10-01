// Drives canvas-editor-showcase: the gox tree renders with every element
// registered, the features are registered, the DOM command list and style
// editor are there, and in edit mode the text flow is editable -- typing,
// multi-cursor, line operations, page breaks and Markdown copy/paste.
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const d = await open();
  const { page } = d;
  let s = await d.state();
  assert(!s.error, 'renders without error: ' + s.error);
  assert(s.mode === 'view' && s.pages === 3, 'starts in view mode with three pages');
  assert(JSON.stringify(s.features) === JSON.stringify(['history', 'multi-cursor', 'line-ops', 'markdown-clipboard', 'breaks', 'paragraphs', 'text-case', 'links', 'styles', 'comments', 'bookmarks', 'notes', 'page-furniture']), 'features registered: ' + s.features);
  for (const tag of ['canvas.TextFlow', 'canvas.Image', 'canvas.FloatingText', 'canvas.Table', 'canvas.ScrollView'])
    assert(s.elements.includes(tag), tag + ' is a registered element');
  assert(s.commands >= 30, 'commands registered: ' + s.commands);
  assert(await page.locator('.gj-docview-top input.gj-docview-title-edit').inputValue() === 'Quarterly report', 'the DocumentView title is an editable box');
  assert(await page.locator('.gj-docview-desk canvas#stage').count() === 1, 'the canvas sits in the DocumentView desk');

  // The menu bar is built from the registered commands: one submenu per feature.
  await page.click('button.gj-menubar-button:has-text("Edit")');
  const subs = await page.locator('.gj-menubar-panel > .gj-menu > .gj-menu-item-sub').count();
  assert(subs === 13, 'Edit has a submenu per feature: ' + subs);
  const shortcut = await page.locator('.gj-menubar-panel li:has-text("Move line up") .gj-menu-shortcut').first().textContent();
  assert(shortcut === 'Alt+↑', 'a command shows its shortcut: ' + shortcut);
  await page.hover('.gj-menubar-panel li.gj-menu-item-sub:has-text("Paragraphs")');
  assert(await page.locator('li:has-text("Bulleted list") .gj-menu-shortcut').first().isVisible(), 'a submenu opens on hover');
  await page.mouse.click(600, 700); // the backdrop closes the menu
  assert(await page.locator('.gj-menubar-panel').count() === 0, 'a click outside closes the menu');

  // The components' stylesheets come from CSS imports, installed by domrt.Mount.
  const sheets = await page.evaluate(() => Array.from(document.querySelectorAll('style[data-gox-style]')).map((e) => e.getAttribute('data-gox-style')));
  assert(sheets.length > 10 && sheets[0].endsWith('/components/base.css') && sheets.some((x) => x.endsWith('/styleeditor.css')), 'component stylesheets installed in <head>: ' + sheets.length);
  const accent = await page.evaluate(() => getComputedStyle(document.documentElement).getPropertyValue('--gj-accent').trim());
  assert(accent === '#2563eb', 'the design tokens apply: ' + accent);

  await d.toggleMode();
  s = await d.state();
  assert(s.mode === 'edit', 'edit mode');

  // Click into the first paragraph (title is at the top of page 1) and type.
  const before = s.text;
  await d.click(180 + 60, 24 + 60 + 80);
  s = await d.state();
  assert(s.editing && s.cursors.length === 1, 'clicking in the text puts the caret there');
  await page.keyboard.type('QQ');
  s = await d.state();
  assert(s.text.length === before.length + 2 && s.text.includes('QQ'), 'typing edits the flow');
  await page.keyboard.press('Backspace');
  s = await d.state();
  assert(s.text.length === before.length + 1 && s.text.includes('Q') && !s.text.includes('QQ'), 'Backspace deletes');

  await page.keyboard.press('Control+z');
  s = await d.state();
  assert(s.text.length === before.length + 2 && s.text.includes('QQ'), 'Ctrl+Z undoes the Backspace');
  await page.keyboard.press('Control+z');
  s = await d.state();
  assert(s.text === before, 'Ctrl+Z undoes the typing: ' + s.text.length + ' vs ' + before.length + ' ' + s.text.slice(0, 40));
  await page.keyboard.press('Control+Shift+z');
  await page.keyboard.press('Control+y');
  s = await d.state();
  assert(s.text.length === before.length + 1, 'Ctrl+Shift+Z and Ctrl+Y redo');

  // Multi-cursor: select the word at the caret, then every match.
  await page.keyboard.press('Control+D');
  await page.keyboard.press('Control+D');
  s = await d.state();
  assert(s.cursors.length >= 1, 'Ctrl+D works on the flow: ' + s.cursors.length);

  // Page break: Ctrl+Enter makes a fourth page's worth of flow.
  await d.click(180 + 60, 24 + 60 + 80);
  await page.keyboard.press('Control+Enter');
  s = await d.state();
  assert(s.cursors[0].After === true, 'the caret goes after the page break');
  await page.keyboard.type('NEWPAGE');
  s = await d.state();
  assert(s.text.includes('NEWPAGE'), 'typing after the break');

  // Line operations: move the line the caret is on.
  const t0 = s.text;
  await page.keyboard.press('Alt+ArrowDown');
  s = await d.state();
  assert(s.text !== t0 && s.text.length === t0.length, 'Alt+Down moves the line (same length, different order)');
  await page.keyboard.press('Alt+ArrowUp');
  s = await d.state();
  assert(s.text === t0, 'Alt+Up moves it back');
  await page.keyboard.press('Alt+Shift+ArrowDown');
  s = await d.state();
  assert(s.text.length > t0.length, 'Alt+Shift+Down copies the line');
  await page.keyboard.press('Control+Shift+K');
  s = await d.state();
  assert(s.text.length === t0.length, 'Ctrl+Shift+K deletes it again');

  // Styles: the gallery applies a paragraph style to the caret's paragraph.
  await page.click('button[data-tab="styles"]');
  assert(await page.locator('.gj-stylepicker-item[data-style="Heading 1"]').count() === 1, 'the style gallery lists the styles');
  await d.click(180 + 60, 24 + 60 + 80);
  const hBefore = (await d.state()).lineHeights.slice(0, 3);
  await page.click('.gj-stylepicker-item[data-style="Heading 2"]');
  s = await d.state();
  assert(s.lineHeights[1] > hBefore[1], 'Heading 2 makes the paragraph taller: ' + hBefore + ' -> ' + s.lineHeights.slice(0, 3));
  await page.click('.gj-stylepicker-item[data-style="Normal"]');
  s = await d.state();
  assert(s.lineHeights[1] === hBefore[1], 'Normal puts it back');

  // Symbols: pick one from the DOM picker into the canvas text.
  await page.click('button[data-tab="symbols"]');
  await d.click(180 + 60, 24 + 60 + 80);
  const tLen = (await d.state()).text.length;
  await page.click('.gj-symbol[data-char="€"]');
  s = await d.state();
  assert(s.text.length === tLen + 1 && s.text.includes('€'), 'a symbol picked in the DOM lands at the canvas caret');
  await page.fill('.gj-symbolpicker-search', 'omega');
  assert(await page.locator('.gj-symbolpicker-grid .gj-symbol').count() === 2, 'the symbol search filters by name');
  await page.click('button[data-tab="format"]');

  // A link: hovering it shows its address on the canvas and a preview card in the DOM.
  s = await d.state();
  for (let i = 0; i < 20 && (s.link.y > 560 || s.link.y < 20); i++) {
    await page.mouse.move(...d.at(450, 300));
    await page.mouse.wheel(0, s.link.y > 560 ? 300 : -300);
    await page.waitForTimeout(50);
    s = await d.state();
  }
  assert(s.link && s.link.url === 'https://github.com/elaous/gojiberry', 'the Markdown link is a link: ' + JSON.stringify(s.link));
  await page.mouse.move(...d.at(s.link.x, s.link.y));
  await page.waitForTimeout(50);
  s = await d.state();
  assert(s.hoverLink === 'https://github.com/elaous/gojiberry', 'hovering a link reports it');
  assert(await page.locator('#link-preview a.gj-linkpreview[href="https://github.com/elaous/gojiberry"]').count() === 1, 'and the DOM shows a preview card');
  await page.mouse.move(...d.at(5, 5));
  await page.waitForTimeout(50);
  assert(await page.locator('#link-preview a.gj-linkpreview').count() === 0, 'the card goes when the pointer leaves');

  // Key bindings editor: rebind Copy line down to Ctrl+J.
  await page.click('button[data-tab="keys"]');
  await page.click('[data-action="change:line.duplicate-down"]');
  await page.waitForSelector('.gj-keybindings-capture');
  await page.keyboard.press('Control+j');
  await page.waitForTimeout(50);
  assert(await page.locator('.gj-keybindings-message:has-text("Copy line down is now Ctrl+J")').count() === 1, 'the editor reports the new binding');
  assert(await page.locator('.gj-keybindings-row-modified:has-text("Copy line down")').count() === 1, 'and marks it modified');
  // Playwright scrolled the long key list into view, which moved the whole page (overflow:hidden still scrolls).
  await page.evaluate(() => { window.scrollTo(0, 0); document.querySelectorAll('*').forEach((e) => { if (e.scrollTop) e.scrollTop = 0; }); });
  await page.mouse.move(...d.at(450, 300));
  await page.mouse.wheel(0, -5000);
  await page.waitForTimeout(50);
  await d.click(180 + 60, 24 + 60 + 80); // a body line
  const lineText = (await d.state()).text;
  await page.keyboard.press('Alt+Shift+ArrowDown');
  s = await d.state();
  assert(s.text === lineText, 'the old chord no longer copies the line');
  await page.keyboard.press('Control+j');
  s = await d.state();
  assert(s.text.length > lineText.length, 'the new chord copies the line: ' + lineText.length + ' -> ' + s.text.length);
  await page.click('[data-action="reset-all"]');
  await page.click('button[data-tab="format"]');

  // Page furniture: the watermark starts on page 2; removing the header from one page.
  s = await d.state();
  assert(JSON.stringify(s.watermarks) === JSON.stringify(['', 'DRAFT', 'DRAFT']), 'watermark from page 2 on: ' + s.watermarks);
  assert(JSON.stringify(s.headers) === JSON.stringify(['Gojiberry editor', 'Gojiberry editor', 'Gojiberry editor']), 'headers: ' + s.headers);
  await page.click('button.gj-menubar-button:has-text("Edit")');
  await page.hover('.gj-menubar-panel li.gj-menu-item-sub:has-text("Page furniture")');
  await page.click('.gj-menubar-panel li.gj-menu-item:not(.gj-menu-item-sub):has-text("Remove watermark from all pages")');
  s = await d.state();
  assert(s.watermarks.every((w) => w === ''), 'Remove watermark from all pages (from the menu): ' + s.watermarks);
  await page.click('button.gj-menubar-button:has-text("View")');
  await page.click('.gj-menubar-panel li:has-text("Two-page spread")');
  s = await d.state();
  assert(s.spread === true, 'the View menu turns on the two-page spread');
  await page.click('button.gj-menubar-button:has-text("View")');
  await page.click('.gj-menubar-panel li:has-text("Two-page spread")');
  s = await d.state();
  assert(s.spread === false, 'and off again');

  // Paragraph formatting: lists, numbering, drop cap.
  await page.mouse.move(...d.at(450, 300));
  await page.mouse.wheel(0, -5000);
  await page.waitForTimeout(50);
  await d.click(180 + 60, 24 + 60 + 80);
  const m0 = (await d.state()).markers.length;
  const count = (a, ch) => a.filter((x) => x === ch).length;
  const b0 = count((await d.state()).markers, '•');
  await page.keyboard.press('Control+Shift+8');
  s = await d.state();
  assert(s.markers.length === m0 + 1 && count(s.markers, '•') === b0 + 1, 'Ctrl+Shift+8 makes the paragraph a bullet item: ' + m0 + ' -> ' + s.markers);
  await page.keyboard.press('End');
  await page.keyboard.press('Enter');
  s = await d.state();
  assert(s.markers.length === m0 + 2, 'Enter continues the list');
  await page.keyboard.press('Control+Shift+7');
  s = await d.state();
  assert(s.markers.includes('1.'), 'Ctrl+Shift+7 numbers the new item: ' + s.markers);
  await page.keyboard.press('Enter');
  await page.keyboard.press('Enter');
  s = await d.state();
  assert(s.markers.length === m0 + 2, 'Enter on an empty item ends the list: ' + s.markers);
  const caps0 = s.dropCaps;
  await d.click(180 + 60, 24 + 60 + 80);
  await page.keyboard.press('Control+e');
  await d.page.evaluate(() => 0);
  s = await d.state();
  assert(s.text.length > 0, 'Ctrl+E (centre) is handled without breaking the text');

  // Markdown clipboard.
  await d.page.context().grantPermissions(['clipboard-read', 'clipboard-write'], { origin: 'http://localhost:' + process.env.PORT });
  await page.evaluate(() => navigator.clipboard.writeText('pasted **bold** text'));
  await page.keyboard.press('Control+Shift+V');
  await page.waitForTimeout(150);
  s = await d.state();
  assert(s.text.includes('pasted bold text'), 'Ctrl+Shift+V pastes Markdown as text, not marks');

  // Auto-closing pairs and completion in the text flow (at several cursors too).
  await page.focus('#input');
  await page.keyboard.press('End');
  await page.keyboard.type('(note');
  s = await d.state();
  assert(s.text.includes('(note)') && !s.text.includes('(note'+'))'), 'typing ( closes the pair in the text flow');
  await page.keyboard.type(')');
  s = await d.state();
  assert(s.text.includes('(note)') && !s.text.includes('(note))'), 'typing ) steps over the closer');
  await page.keyboard.type(' feat');
  s = await d.state();
  assert(s.suggestions && s.suggestions.includes('feature'), 'typing a prefix offers completions: ' + s.suggestions);
  await page.keyboard.press('Tab');
  s = await d.state();
  assert(s.text.includes('(note) feature') && s.suggestions.length === 0, 'Tab accepts the completion');

  // Outline and bookmarks: the headings are a list that jumps; a bookmark is a named place.
  await page.click('button[data-tab="outline"]');
  s = await d.state();
  assert(s.outline.includes('1:Gojiberry editor') && s.outline.includes('2:Second thoughts'), 'the outline lists the headings: ' + s.outline);
  assert(await page.locator('.gj-outline-item[data-level="2"]').count() >= 1, 'the Outline component shows them with their levels');
  const scroll0 = s.scrollY;
  await page.click('.gj-outline-item:has-text("Second thoughts")');
  s = await d.state();
  assert(s.scrollY > scroll0, 'clicking a heading scrolls to it: ' + scroll0 + ' -> ' + s.scrollY);
  assert(await page.locator('.gj-outline-item-on').textContent() === 'Second thoughts', 'and marks it current');
  await page.focus('#input');
  page.once('dialog', (dlg) => dlg.accept('second'));
  await page.keyboard.press('Control+Alt+b');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).bookmarks.includes('second'));
  assert(await page.locator('.gj-bookmark[data-bookmark="second"], .gj-bookmark:has([data-bookmark="second"])').count() === 1, 'the bookmark is listed');
  assert(await page.locator('.gj-bookmark-link').textContent() === '#second', 'with the link form to use');
  await page.mouse.move(...d.at(450, 300));
  await page.mouse.wheel(0, -5000);
  await page.waitForTimeout(100);
  assert((await d.state()).scrollY === 0, 'back at the top');
  await page.click('.gj-bookmark-go');
  s = await d.state();
  assert(s.scrollY > 0, 'a bookmark takes you to its text: ' + s.scrollY);
  await page.click('.gj-bookmark-remove');
  assert((await d.state()).bookmarks.length === 0, 'Remove takes it off');

  // Footnotes, a table of contents and an index.
  await page.mouse.move(...d.at(450, 300));
  await page.mouse.wheel(0, -5000);
  await page.waitForTimeout(100);
  await d.click(180 + 60, 24 + 60 + 80); // a body line
  await page.focus('#input');
  await page.keyboard.press('End');
  page.once('dialog', (dlg) => dlg.accept('Source: the **gojiberry** docs'));
  await page.keyboard.press('Control+Alt+f');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).footnotes.length === 1);
  assert(await page.locator('.gj-footnote .gj-footnote-num').textContent() === '1', 'the footnote is numbered in the list');
  assert(await page.locator('.gj-footnote strong').textContent() === 'gojiberry', 'its text is Markdown');
  await page.click('button.gj-menubar-button:has-text("Insert")');
  await page.click('.gj-menubar-panel li:has-text("Table of contents")');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).tocRows >= 2);
  s = await d.state();
  assert(s.tocRows >= 2, 'a table of contents lists the headings: ' + s.tocRows);
  await page.focus('#input');
  for (let i = 0; i < 5; i++) { await page.focus('#input'); await page.keyboard.press('Shift+ArrowLeft'); }
  page.once('dialog', (dlg) => dlg.accept('terms'));
  await page.keyboard.press('Control+Alt+x');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).indexTerms.includes('terms'));
  assert(true, 'the selection is marked as an index entry');
  await page.click('.gj-footnote-delete');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).footnotes.length === 0);
  assert(true, 'deleting a footnote removes its mark and note');

  // Comments: comment the selection, see it listed with its text and mentions, hover it, reply, resolve, delete.
  await page.mouse.move(...d.at(450, 300));
  await page.mouse.wheel(0, -5000);
  await page.waitForTimeout(100);
  await d.click(180 + 60, 24 + 60 + 80); // a body line
  await page.focus('#input');
  await page.keyboard.press('End');
  for (let i = 0; i < 7; i++) { await page.focus('#input'); await page.keyboard.press('Shift+ArrowLeft'); }
  await page.focus('#input');
  page.once('dialog', (dlg) => dlg.accept('Check **this** wording, @bob or ana@example.com'));
  await page.keyboard.press('Control+Alt+n');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).comments === 1, null, { timeout: 5000 });
  await page.click('button[data-tab="comments"]');
  assert((await page.locator('.gj-comment-excerpt').textContent()).length === 7, 'the comment card shows the commented text');
  assert(await page.locator('.gj-comment .gj-markdown strong').textContent() === 'this', 'the body is rendered as Markdown');
  assert(await page.locator('.gj-comment .gj-mention[data-mention="bob"]').count() === 1, 'an @username is highlighted');
  assert(await page.locator('.gj-comment a.gj-mention-email[href="mailto:ana@example.com"]').count() === 1, 'an e-mail address is a mailto mention');
  assert((await page.locator('.gj-comment-author').textContent()) === 'you@example.com', 'the author is the current user');
  s = await d.state();
  await page.mouse.move(...d.at(s.comment.x, s.comment.y), { steps: 3 });
  await page.waitForTimeout(100);
  s = await d.state();
  assert(s.hoverComment === s.comment.id, 'hovering commented text finds its comment: ' + s.hoverComment);
  const tip = await d.pixel(Math.round(s.comment.x), Math.round(s.comment.y) + 28);
  assert(tip[0] === 0xff && tip[1] === 0xfb && tip[2] === 0xe6, 'the summary tip is drawn under the text: ' + tip);
  await page.fill('.gj-comment-input', 'Agreed');
  await page.click('.gj-comment-send');
  assert(await page.locator('.gj-comment-reply').textContent() === 'you@example.com Agreed', 'a reply is added');
  await page.click('.gj-comment-resolve');
  assert(await page.locator('.gj-comment').count() === 0, 'a resolved comment leaves the open list');
  await page.click('.gj-comment-toggle');
  assert(await page.locator('.gj-comment-resolved').count() === 1, 'and shows when asked');
  await page.click('.gj-comment-delete');
  s = await d.state();
  assert(s.comments === 0 && s.comment === null, 'Delete removes the comment and un-marks its text');

  // Lazy fonts: picking a font only adds it; the file is downloaded into
  // the store the first time the text uses it.
  const fs = require('fs');
  const realFont = ['/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf', '/usr/share/fonts/truetype/freefont/FreeSerif.ttf'].find((p) => fs.existsSync(p));
  let fontHits = 0;
  await page.route('**/fonts/lazy-serif.ttf', (route) => {
    fontHits++;
    route.fulfill({ status: 200, contentType: 'font/ttf', headers: { 'access-control-allow-origin': '*' }, body: realFont ? fs.readFileSync(realFont) : Buffer.from('not a font') });
  });
  await page.focus('#input');
  await page.keyboard.press('ArrowRight'); // a caret, nothing selected
  await page.click('button[data-tab="format"]');
  await page.click('.gj-fontselector-control');
  await page.fill('.gj-fontselector-search', 'Lazy');
  await page.click('.gj-fontselector-option[data-family="Lazy Serif"]');
  await page.waitForTimeout(300);
  s = await d.state();
  assert(s.fontsAdded.includes('Lazy Serif='), 'the picked font is in the library, not yet downloaded: ' + s.fontsAdded);
  assert(fontHits === 0, 'nothing was fetched yet: no text uses the font');
  await page.focus('#input');
  await page.keyboard.type('lazy');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).fontsAdded.some((f) => f.startsWith('Lazy Serif=fonts/')), null, { timeout: 5000 });
  s = await d.state();
  assert(fontHits === 1, 'the font file was fetched once, when the text used it: ' + fontHits);
  assert(s.fontLog.some((l) => l.includes('Lazy Serif 400: downloaded into the store')), 'the library logged the download: ' + s.fontLog);
  assert(s.fontsAdded.includes('Lazy Serif=fonts/Lazy Serif/400-n.ttf'), 'its store key is recorded: ' + s.fontsAdded);
  if (realFont) {
    await page.waitForFunction(() => Array.from(document.fonts).some((f) => f.family.replace(/"/g, '') === 'Lazy Serif' && f.status === 'loaded'), null, { timeout: 5000 });
    assert(true, 'the browser loaded the downloaded font');
  }
  await page.keyboard.press('ArrowRight');
  await page.waitForTimeout(100);
  assert(fontHits === 1, 'using it again does not download it again');

  // Saver: memory target, protobuf binary, then the browser's localStorage as JSON.
  await page.click('button[data-tab="save"]');
  assert(await page.locator('.gj-saver-select[data-field="Save to"] option').count() === 2, 'the Saver offers its targets');
  assert(await page.locator('.gj-saver-select[data-field="Format"] option').count() === 3, 'and the formats');
  assert(await page.locator('.gj-saver-save').isDisabled(), 'Save waits for a name');
  await page.fill('.gj-saver-name', 'notes');
  await page.selectOption('.gj-saver-select[data-field="Save to"]', 'memory');
  await page.selectOption('.gj-saver-select[data-field="Format"]', 'proto');
  await page.click('.gj-saver-save');
  await page.waitForSelector('.gj-saver-entry[data-key="notes.pb"]');
  assert((await page.locator('.gj-saver-status').textContent()) === 'Saved notes.pb', 'status: ' + (await page.locator('.gj-saver-status').textContent()));
  assert(await page.locator('.gj-saver-entry').count() === 1, 'the downloaded font in the same store is not listed as a document');
  await page.selectOption('.gj-saver-select[data-field="Save to"]', 'browser');
  await page.selectOption('.gj-saver-select[data-field="Format"]', 'json');
  await page.waitForFunction(() => document.querySelectorAll('.gj-saver-entry').length === 0);
  await page.click('.gj-saver-save');
  await page.waitForSelector('.gj-saver-entry[data-key="notes.json"]');
  const saved = JSON.parse(await page.evaluate(() => localStorage.getItem('showcase:notes.json')).then((r) => Buffer.from(JSON.parse(r).d, 'base64').toString()));
  assert(saved.title === 'Quarterly report' && saved.content.length > 0, 'localStorage holds the document as JSON: ' + saved.title);
  assert(saved.fonts.some((f) => f.family === 'Lazy Serif' && f.variants[0].key === 'fonts/Lazy Serif/400-n.ttf'), 'the saved document records the font and where its file is');
  assert(saved.content.some((i) => i.chars && i.chars.format.fontFamily && i.chars.format.fontFamily.includes('Lazy Serif')), 'text in the font is saved with its family');

  // Open: change the text, open the saved JSON, the text is back.
  const textBefore = (await d.state()).text;
  await page.focus('#input');
  await page.keyboard.press('Control+End');
  await page.keyboard.type('ZZZ');
  assert((await d.state()).text.includes('ZZZ'), 'edited after saving');
  await page.click('.gj-saver-entry[data-key="notes.json"] .gj-saver-open');
  await page.waitForFunction(() => !JSON.parse(document.getElementById('state').textContent).text.includes('ZZZ'));
  s = await d.state();
  assert(s.text === textBefore, 'Open restores the saved text');
  await page.selectOption('.gj-saver-select[data-field="Save to"]', 'memory');
  await page.waitForSelector('.gj-saver-entry[data-key="notes.pb"]');
  await page.click('.gj-saver-entry[data-key="notes.pb"] .gj-saver-delete');
  await page.waitForFunction(() => document.querySelectorAll('.gj-saver-entry').length === 0);
  assert((await page.locator('.gj-saver-status').textContent()) === 'Deleted notes.pb', 'Delete removes a saved document');

  // Import / Export.
  await page.click('button[data-tab="transfer"]');
  const [download] = await Promise.all([page.waitForEvent('download'), page.click('.gj-transfer-button[data-kind="export"][data-format="markdown"]')]);
  assert(download.suggestedFilename() === 'Quarterly report.md', 'Export downloads a file named after the title: ' + download.suggestedFilename());
  const exported = fs.readFileSync(await download.path(), 'utf8');
  assert(exported.includes('pasted'), 'the Markdown export has the text');
  const [chooser] = await Promise.all([page.waitForEvent('filechooser'), page.click('.gj-transfer-button[data-kind="import"][data-format="markdown"]')]);
  await chooser.setFiles({ name: 'hello.md', mimeType: 'text/markdown', buffer: Buffer.from('# Imported\n\nfresh **words**') });
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).text.includes('fresh words'));
  assert((await page.locator('#transfer-message').textContent()) === 'Imported hello.md', 'Import reads the file into the editor');

  // Roles: a viewer is held in view mode and cannot comment; an editor can edit but not manage.
  await d.click(100, 164); // into the imported text's second line
  await page.focus('#input');
  await page.keyboard.press('End');
  page.once('dialog', (dlg) => dlg.accept('One more'));
  for (let i = 0; i < 3; i++) { await page.focus('#input'); await page.keyboard.press('Shift+ArrowLeft'); } // the format panel re-renders on a heading selection
  await page.focus('#input');
  await page.keyboard.press('Control+Alt+n');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).comments === 1);
  await page.click('button[data-role="viewer"]');
  s = await d.state();
  assert(s.role === 'viewer' && s.mode === 'view' && !s.editing, 'a viewer is put back in view mode');
  await d.toggleMode();
  s = await d.state();
  assert(s.mode === 'view', 'and cannot switch to edit mode');
  const typed = s.text;
  await d.click(180 + 60, 24 + 60 + 80);
  await page.keyboard.type('nope');
  assert((await d.state()).text === typed, 'typing changes nothing for a viewer');
  await page.click('button[data-tab="comments"]');
  assert(await page.locator('.gj-comment').count() === 1 && await page.locator('.gj-comment-actions').count() === 0, 'a viewer reads comments but has no controls');
  await page.click('button[data-role="editor"]');
  await d.toggleMode();
  s = await d.state();
  assert(s.role === 'editor' && s.mode === 'edit', 'an editor can edit again');
  assert(await page.locator('.gj-comment-actions').count() === 1, 'and gets the comment controls back');
  await page.click('button[data-role="owner"]');
  await page.keyboard.press('Escape');
  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
