// Drives canvas-editor-showcase's accessibility and translation: the canvas has
// a screen-reader copy of the document with its structure, a live region says
// what happened, and the interface language switches from the View menu.
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const d = await open('?lang=en');
  const { page } = d;
  assert(await page.locator('#stage').getAttribute('role') === 'textbox' && await page.locator('#stage').getAttribute('aria-label') === 'Document', 'the canvas is a labelled text box');
  assert(await page.locator('#input').getAttribute('aria-describedby') === 'a11y-text', 'the editing input is described by the document text');
  const doc = page.locator('#a11y-text');
  assert(await doc.getAttribute('role') === 'document' && await doc.getAttribute('aria-label') === 'Document text', 'the document text is a labelled region');
  assert(await doc.locator('h1').first().textContent() === 'Gojiberry editor', 'headings keep their level: ' + await doc.locator('h1').first().textContent());
  assert(await doc.locator('h2').count() >= 1 && await doc.locator('ul li').count() >= 2 && await doc.locator('p').count() >= 3, 'with paragraphs and lists');
  assert(await doc.locator('table').count() === 1 && await doc.locator('table th').count() === 2, 'and the table as a table with its header');
  const box = await doc.boundingBox();
  assert(box.width <= 1 && box.height <= 1, 'it is hidden from sight, not from readers');
  const display = await doc.evaluate((el) => getComputedStyle(el).display);
  assert(display !== 'none', 'and not display:none');

  // The text follows the edits, and the selection is in it.
  await d.toggleMode();
  assert(await page.locator('.gj-announcer').textContent() === 'Edit mode', 'switching mode is announced');
  await d.click(240, 164);
  await page.focus('#input');
  await page.keyboard.press('End');
  await page.keyboard.type(' ZZTOP');
  await page.waitForFunction(() => document.getElementById('a11y-text').textContent.includes('ZZTOP'));
  assert(true, 'typed text reaches the screen-reader copy');
  await page.mouse.dblclick(...d.at(240, 164));
  await page.waitForFunction(() => document.querySelector('.gj-a11y-selection'));
  assert((await page.locator('.gj-a11y-selection').textContent()).startsWith('Selected: '), 'the selection is announced in it');

  // Find announces how many matches.
  await page.focus('#input');
  await page.keyboard.press('Control+f');
  await page.fill('.gj-find-input', 'the');
  await page.waitForFunction(() => /matches/.test(document.querySelector('.gj-announcer').textContent));
  assert(/^\d+ matches$/.test(await page.locator('.gj-announcer').textContent()), 'find says how many matches: ' + await page.locator('.gj-announcer').textContent());
  await page.click('.gj-find-close');

  // Translation of the interface.
  assert(await page.locator('html').getAttribute('lang') === 'en' && await page.locator('html').getAttribute('dir') === 'ltr', 'the page says it is English, left to right');
  await page.click('button.gj-menubar-button:has-text("View")');
  await page.hover('.gj-menubar-panel li.gj-menu-item-sub:has-text("Interface language")');
  await page.click('.gj-menubar-panel .gj-menu-label:text-is("Español")');
  await page.waitForFunction(() => document.documentElement.lang === 'es');
  assert(await page.locator('button[data-tab="find"]').textContent() === 'Buscar', 'the tabs are in Spanish: ' + await page.locator('button[data-tab="find"]').textContent());
  assert(await page.locator('button.gj-menubar-button:has-text("Ver")').count() === 1, 'and the menus');
  assert(/páginas · \d+ palabras · \d+ caracteres/.test(await page.locator('.gj-docview-status').textContent()), 'and the status bar: ' + await page.locator('.gj-docview-status').textContent());
  await page.click('button[data-tab="save"]');
  assert(await page.locator('.gj-saver-save').textContent() === 'Guardar', 'and the components');
  assert(await page.locator('#a11y-text').getAttribute('aria-label') === 'Texto del documento' && await page.locator('#stage').getAttribute('aria-label') === 'Documento', 'including what a screen reader hears');
  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
