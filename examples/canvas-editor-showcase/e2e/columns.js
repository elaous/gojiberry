// Drives canvas-editor-showcase's column commands from the Edit menu: a new
// region of columns starts at the caret's paragraph, and Undo takes it away.
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const d = await open();
  const { page } = d;
  assert((await d.state()).columns === 0, 'the text starts in the base layout');
  await d.toggleMode();
  await d.click(240, 164);
  await page.focus('#input');
  const text = (await d.state()).text;
  await page.click('button.gj-menubar-button:has-text("Edit")');
  await page.hover('.gj-menubar-panel li.gj-menu-item-sub:has-text("Columns")');
  await page.click('.gj-menubar-panel li:has-text("Two columns"):not(:has-text("wide")):not(:has-text("narrow"))');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).columns === 2);
  assert((await d.state()).text === text, 'Two columns sets two columns from the caret and leaves the text alone');
  await page.focus('#input');
  await page.keyboard.press('Control+z');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).columns === 0);
  assert((await d.state()).text === text, 'Undo takes the columns away again');
  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
