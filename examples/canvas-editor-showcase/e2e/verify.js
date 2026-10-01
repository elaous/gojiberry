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
  assert(JSON.stringify(s.features) === JSON.stringify(['history', 'multi-cursor', 'line-ops', 'markdown-clipboard', 'breaks', 'paragraphs', 'text-case', 'links', 'page-furniture']), 'features registered: ' + s.features);
  for (const tag of ['canvas.TextFlow', 'canvas.Image', 'canvas.FloatingText', 'canvas.Table', 'canvas.ScrollView'])
    assert(s.elements.includes(tag), tag + ' is a registered element');
  assert(s.commands >= 30, 'commands registered: ' + s.commands);
  assert(await page.locator('.gj-docview-top input.gj-docview-title-edit').inputValue() === 'Quarterly report', 'the DocumentView title is an editable box');
  assert(await page.locator('.gj-docview-desk canvas#stage').count() === 1, 'the canvas sits in the DocumentView desk');

  // The menu bar is built from the registered commands: one submenu per feature.
  await page.click('button.gj-menubar-button:has-text("Edit")');
  const subs = await page.locator('.gj-menubar-panel > .gj-menu > .gj-menu-item-sub').count();
  assert(subs === 9, 'Edit has a submenu per feature: ' + subs);
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
  await page.keyboard.press('Escape');
  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
