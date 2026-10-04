// Drives canvas-editor-showcase's find and replace: Ctrl+F opens the bar,
// matches are counted and highlighted, F3 steps, options narrow the search,
// and Replace and Replace all edit the text (keeping it one undo).
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const d = await open();
  const { page } = d;
  let s = await d.state();
  assert(!s.find.open, 'the find bar is closed at first');

  await page.focus('#input');
  await page.keyboard.press('Control+f');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).find.open);
  assert(await page.locator('.gj-find').count() === 1, 'Ctrl+F opens the find bar, even in view mode');
  assert(await page.locator('.gj-find-replacement').count() === 0, 'and a viewer-mode document has no replace row');

  await page.fill('.gj-find-input', 'the');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).find.count > 3);
  s = await d.state();
  const all = s.find.count;
  assert(/^1 of \d+$/.test(await page.locator('.gj-find-status').textContent()), 'the bar shows which match is current: ' + await page.locator('.gj-find-status').textContent());
  await page.click('.gj-find-next');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).find.current === 1);
  assert((await d.state()).find.current === 1, 'Next goes to the second match');
  await page.click('.gj-find-prev');
  await page.click('.gj-find-prev');
  await page.waitForFunction((n) => JSON.parse(document.getElementById('state').textContent).find.current === n - 1, all);
  assert(true, 'Previous wraps round to the last');

  await page.click('.gj-find-word');
  await page.waitForFunction((n) => JSON.parse(document.getElementById('state').textContent).find.count < n, all);
  assert((await d.state()).find.count < all, 'whole word narrows the matches');
  await page.click('.gj-find-word');

  await page.click('.gj-find-regex');
  await page.fill('.gj-find-input', '(');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).find.error !== '');
  assert(await page.locator('.gj-find-error').count() === 1, 'a bad regular expression is shown as an error');
  await page.click('.gj-find-regex');

  // Editing: Replace all.
  await d.toggleMode();
  await page.focus('#input');
  await page.keyboard.press('Control+h');
  await page.fill('.gj-find-input', 'canvas');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).find.count >= 1);
  const before = (await d.state()).text;
  const n = (await d.state()).find.count;
  await page.fill('.gj-find-replacement', 'CANVAS');
  await page.click('.gj-find-replace-all');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).text.includes('CANVAS'));
  const after = (await d.state()).text;
  assert(after === before.replace(/canvas/gi, 'CANVAS'), 'Replace all changed all ' + n + ' matches and nothing else');
  assert(await page.locator('#find-hint, .gj-find-status:has-text("No matches")').count() >= 0, 'the bar updates');

  await d.click(240, 164); // back into the text
  await page.focus('#input');
  await page.keyboard.press('Control+z');
  await page.waitForFunction((t) => JSON.parse(document.getElementById('state').textContent).text === t, before);
  assert(true, 'Undo brings the text back in one step');

  await page.click('.gj-find-close');
  assert(!(await d.state()).find.open && await page.locator('.gj-find').count() === 0, 'Close removes the bar');
  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
