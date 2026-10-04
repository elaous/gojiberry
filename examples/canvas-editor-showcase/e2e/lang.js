// Drives canvas-editor-showcase's Language tab against fake backends: detecting
// the language, translating the selection, and checking grammar.
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const q = `?translate=${encodeURIComponent(process.env.TRANSLATE_URL)}&grammar=${encodeURIComponent(process.env.GRAMMAR_URL)}&model=m1`;
  const d = await open(q);
  const { page } = d;
  await d.toggleMode();
  await page.click('button[data-tab="language"]');
  assert(/Language: English/.test(await page.locator('.gj-lang-detected').textContent()), 'the language is detected: ' + await page.locator('.gj-lang-detected').textContent());
  assert(await page.locator('.gj-lang-target').count() === 1 && await page.locator('.gj-lang-check').count() === 1, 'both backends are offered');
  assert((await d.state()).language.translate && (await d.state()).language.grammar, 'the page knows both are set up');

  // Translate a selected word.
  assert(await page.locator('.gj-lang-translate').isDisabled(), 'translate waits for a selection');
  await page.mouse.dblclick(...d.at(240, 164));
  await page.click('button[data-tab="stats"]');
  await page.click('button[data-tab="language"]');
  await page.selectOption('.gj-lang-target', 'FR');
  await page.click('.gj-lang-translate');
  await page.waitForSelector('.gj-lang-translation');
  const translated = await page.locator('.gj-lang-translation').textContent();
  assert(/\[FR\]/.test(translated), 'the selection is translated by the server: ' + translated);
  const before = (await d.state()).text;
  await page.click('.gj-lang-use');
  await page.waitForFunction((t) => JSON.parse(document.getElementById('state').textContent).text !== t, before);
  assert((await d.state()).text.includes('[FR]'), 'Replace the selection puts the translation in the text');

  // Grammar: the fake model flags the first "canvas" and offers "canvases".
  await page.click('.gj-lang-check');
  await page.waitForSelector('.gj-lang-issue');
  assert((await d.state()).language.issues === 1, 'an issue is found');
  const label = await page.locator('.gj-lang-apply').textContent();
  assert(label.includes('canvases'), 'with the fix on offer: ' + label);
  await page.click('.gj-lang-apply');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).text.includes('canvases'));
  assert(await page.locator('.gj-lang-issue').count() === 0, 'applying it changes the text and clears the issue');
  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
