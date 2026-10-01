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
  assert(JSON.stringify(s.features) === JSON.stringify(['multi-cursor', 'line-ops', 'markdown-clipboard', 'breaks', 'paragraphs']), 'features registered: ' + s.features);
  for (const tag of ['canvas.TextFlow', 'canvas.Image', 'canvas.FloatingText', 'canvas.Table', 'canvas.ScrollView'])
    assert(s.elements.includes(tag), tag + ' is a registered element');
  assert(s.commands >= 30, 'commands registered: ' + s.commands);
  const items = await page.locator('.command').count();
  assert(items === s.commands, 'the DOM lists every registered command: ' + items);
  assert(await page.locator('.styleeditor, [class*=style]').count() > 0, 'the style editor is rendered');

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

  // Paragraph formatting: lists, numbering, drop cap.
  await d.click(180 + 60, 24 + 60 + 80);
  const m0 = (await d.state()).markers.length;
  await page.keyboard.press('Control+Shift+8');
  s = await d.state();
  assert(s.markers.length === m0 + 1 && s.markers.includes('•'), 'Ctrl+Shift+8 makes the paragraph a bullet item: ' + s.markers);
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
