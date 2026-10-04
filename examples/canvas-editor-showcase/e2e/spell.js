// Drives canvas-editor-showcase's spell check and hyphenation: the Spelling tab
// lists unknown words with suggestions, picks and Add to dictionary change the
// text and persist, and the View menu turns hyphenation on.
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const d = await open();
  const { page } = d;
  assert(!(await d.state()).spell.enabled, 'spell check is off at first');
  await d.toggleMode();

  await page.click('button[data-tab="spell"]');
  await page.check('.gj-spell-enable');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).spell.enabled, null, { timeout: 15000 });
  await page.waitForSelector('.gj-spell-item[data-word="Gojiberry"]');
  assert(await page.locator('.gj-spell-item[data-word="Gojiberry"]').count() === 1, 'an unknown word is listed');
  assert((await d.state()).spell.spans > 0, 'and marked on the canvas');
  assert(await page.locator('.gj-spell-info').textContent() === 'Dictionary: English', 'the dictionary is named');

  // A typo: type "teh" at the end of a line and take the suggestion.
  await d.click(240, 164);
  await page.focus('#input');
  await page.keyboard.press('End');
  await page.keyboard.type(' teh');
  await page.waitForSelector('.gj-spell-item[data-word="teh"]');
  const suggestions = await page.locator('.gj-spell-item[data-word="teh"] .gj-spell-suggestion').allTextContents();
  assert(suggestions.includes('the'), 'the typo comes with suggestions: ' + suggestions);
  await page.click('.gj-spell-item[data-word="teh"] .gj-spell-suggestion:text-is("the")');
  await page.waitForFunction(() => !JSON.parse(document.getElementById('state').textContent).text.includes(' teh'));
  assert((await d.state()).text.includes(' the'), 'picking a suggestion replaces the word');
  assert(await page.locator('.gj-spell-item[data-word="teh"]').count() === 0, 'and it leaves the list');

  // Add to dictionary persists.
  await page.click('.gj-spell-item[data-word="Gojiberry"] .gj-spell-add');
  await page.waitForFunction(() => document.querySelectorAll('.gj-spell-item[data-word="Gojiberry"]').length === 0);
  assert(JSON.parse(await page.evaluate(() => localStorage.getItem('showcase:dictionary'))).includes('gojiberry'), 'an added word is kept in the browser');
  await page.reload();
  await page.waitForFunction(() => document.getElementById('state') && document.getElementById('state').textContent !== '(pending)');
  await page.click('button[data-tab="spell"]');
  await page.check('.gj-spell-enable');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).spell.enabled, null, { timeout: 15000 });
  await page.waitForTimeout(300);
  assert(await page.locator('.gj-spell-item[data-word="Gojiberry"]').count() === 0, 'and still known after a reload');

  // Hyphenation: a View menu switch.
  assert((await d.state()).hyphenate === false, 'hyphenation is off at first');
  await page.click('button.gj-menubar-button:has-text("View")');
  await page.click('.gj-menubar-panel li:has-text("Hyphenate long words")');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).hyphenate === true);
  assert(true, 'the View menu turns hyphenation on');
  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
