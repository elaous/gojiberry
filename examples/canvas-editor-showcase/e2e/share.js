// Drives canvas-editor-showcase's word count and "share the selection": the
// counts in the status bar and the Stats tab (for the text and for the
// selection), and a link to the selected text that selects it again when
// opened.
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const d = await open();
  const { page } = d;
  let s = await d.state();
  assert(s.stats.words > 50 && s.stats.characters > s.stats.words && s.stats.charactersNoSpaces < s.stats.characters && s.stats.paragraphs >= 4, 'counts: ' + JSON.stringify(s.stats));
  assert(s.stats.words === s.text.split(/\s+/).filter(Boolean).length, 'the word count agrees with the text');
  assert(/\d+ of 3 pages · \d+ words · \d+ characters/.test(await page.locator('.gj-docview-status').textContent()), 'the status bar shows pages, words and characters');
  assert(!s.stats.hasSelection, 'nothing is selected at first');

  await page.click('button[data-tab="stats"]');
  assert(await page.locator('#stats tr').count() === 8, 'the Stats tab lists the counts');

  await page.mouse.dblclick(...d.at(240, 164)); // selects a word
  s = await d.state();
  assert(s.stats.hasSelection && s.stats.selWords === 1, 'selecting a word counts it: ' + JSON.stringify(s.stats));
  await page.waitForFunction(() => document.querySelector('.gj-docview-status').textContent.startsWith('Selection: 1 words'));
  assert(true, 'the status bar shows the selection');

  await page.click('button[data-tab="share"]');
  const link = await page.locator('#share-link').inputValue();
  const quote = await page.locator('#share-quote').textContent();
  assert(link.includes('sel=') && link.includes('q=') && !link.includes('token'), 'the link carries the selection: ' + link);
  assert(quote.length > 0 && !/\s/.test(quote), 'and shows the quoted word: ' + quote);

  const other = await open(link.slice(link.indexOf('?')));
  await other.page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).stats.hasSelection, null, { timeout: 8000 }).catch(async (e) => { throw new Error('link not applied: errors=' + other.errors.join(';') + ' state=' + JSON.stringify(await other.state()).slice(0, 300) + ' url=' + other.page.url()); });
  s = await other.state();
  assert(s.stats.selWords === 1 && s.tab === 'share', 'opening the link selects the same word');
  assert((await other.page.locator('#share-quote').textContent()) === quote, 'and it is the quoted one');
  await other.close();

  await page.click('button[data-tab="stats"]'); // clicking elsewhere keeps the selection
  await d.click(240, 300);
  await page.waitForFunction(() => !JSON.parse(document.getElementById('state').textContent).stats.hasSelection);
  await page.click('button[data-tab="share"]');
  assert(await page.locator('#share-hint').count() === 1, 'with nothing selected the Share tab says to select text');
  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
