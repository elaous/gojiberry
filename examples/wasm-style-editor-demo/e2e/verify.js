// Drives wasm-style-editor-demo in headless Chromium: choose a font in the
// FontSelector (search, source chips, recent), toggle decorations, edit the
// size, and check both the demo's mirrored canvas.Format and the live
// preview's computed style.
const { chromium } = require('playwright');

function assert(cond, msg) {
  if (!cond) throw new Error('FAIL: ' + msg);
  console.log('ok:', msg);
}

(async () => {
  const browser = await chromium.launch({
    executablePath: process.env.PLAYWRIGHT_CHROMIUM || '/opt/pw-browsers/chromium',
    args: ['--no-sandbox'],
  });
  const page = await browser.newPage({ viewport: { width: 900, height: 900 } });
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(`http://localhost:${process.env.PORT}/index.html`);
  await page.waitForFunction(() => document.getElementById('state').textContent !== '(pending)', null, { timeout: 15000 });
  const state = async () => JSON.parse(await page.textContent('#state'));
  const preview = (prop) => page.$eval('[data-preview]', (el, p) => getComputedStyle(el)[p], prop);

  let s = await state();
  assert(s.format.FontFamily === '"Georgia", serif' && s.fonts >= 19, 'starts with Georgia and a merged catalog of ' + s.fonts + ' fonts');
  assert((await preview('fontFamily')).includes('Georgia'), 'the preview uses the family');
  assert(await preview('fontSize') === '18px', 'unset size shows the base size');

  // Open the selector, search, filter by source, pick.
  await page.click('.gj-fontselector-control');
  assert((await state()).open && await page.isVisible('.gj-fontselector-panel'), 'clicking the control opens the panel');
  const optionCount = () => page.$$eval('.gj-fontselector-option', (els) => els.length);
  const all = await optionCount();
  await page.fill('.gj-fontselector-search', 'fira');
  assert((await state()).query === 'fira' && await optionCount() === 1, 'searching narrows the list to Fira Code');
  await page.fill('.gj-fontselector-search', '');
  await page.click('.gj-fontselector-chip[data-source="Project fonts"]');
  assert(await optionCount() === 2, 'the source chip limits the list to the two project fonts');
  await page.click('.gj-fontselector-chip[data-source=""]');
  assert(await optionCount() === all, 'All brings the list back');
  await page.click('[data-family="Playfair Display"]');
  s = await state();
  assert(s.format.FontFamily === '"Playfair Display", serif' && !s.open, 'choosing a font sets the format and closes the panel: ' + s.format.FontFamily);
  assert(s.recent[0] === 'Playfair Display', 'it is remembered as recent');
  assert((await page.textContent('.gj-fontselector-label')) === 'Playfair Display', 'the control shows the choice');

  // Reopening lists the recent font first.
  await page.click('.gj-fontselector-control');
  assert((await page.textContent('.gj-fontselector-group')) === 'Recent', 'a Recent group appears');
  assert((await page.$eval('.gj-fontselector-option', (el) => el.dataset.family)) === 'Playfair Display', 'recent fonts come first');
  await page.click('.gj-fontselector-control');

  // Toggles and fields.
  await page.click('[data-action="bold"]');
  await page.click('[data-action="italic"]');
  await page.click('[data-action="underline"]');
  s = await state();
  assert(s.format.FontWeight === 700 && s.format.FontStyle === 'italic' && s.format.Underline, 'bold, italic and underline set the format');
  assert(await preview('fontWeight') === '700' && await preview('fontStyle') === 'italic' && (await preview('textDecorationLine')).includes('underline'), 'the preview shows them');
  assert(await page.getAttribute('[data-action="bold"]', 'aria-pressed') === 'true', 'toggles show as pressed');

  await page.click('[data-action="superscript"]');
  await page.click('[data-action="subscript"]');
  s = await state();
  assert(s.format.Subscript && !s.format.Superscript && await preview('verticalAlign') === 'sub', 'superscript and subscript exclude each other');
  await page.click('[data-action="subscript"]');

  await page.fill('[data-field="size"]', '30');
  assert((await state()).format.FontSize === 30 && await preview('fontSize') === '30px', 'size edits the format and preview');
  await page.selectOption('[data-field="weight"]', '300');
  assert((await state()).format.FontWeight === 300, 'weight select');
  await page.fill('[data-field="color"]', '#aa0000');
  assert((await state()).format.Color === '#aa0000' && await preview('color') === 'rgb(170, 0, 0)', 'text color');
  await page.click('[data-clear="color"]');
  assert((await state()).format.Color === '', 'clear color returns to inheriting');
  await page.fill('[data-field="background"]', '#00ff00');
  assert((await state()).format.Background === '#00ff00', 'highlight');

  // Device fonts: headless Chromium may or may not offer the API; either way
  // the demo reports an outcome and keeps working.
  await page.click('#load-device-fonts');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).status !== '', null, { timeout: 20000 });
  s = await state();
  assert(/Loaded device fonts|Could not list device fonts/.test(s.status), 'loading device fonts reports: ' + s.status);

  assert(errors.length === 0, 'no page errors: ' + errors.join('; '));
  await browser.close();
  console.log('ALL CHECKS PASSED');
})().catch((e) => { console.error(e.message || e); process.exit(1); });
