// Drives canvas-editor-showcase's document lifecycle in the Save tab: versions
// (snapshot, edit, go back), duplicate, tags and filtering by them, and the
// trash with restore and delete forever.
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const d = await open();
  const { page } = d;
  await page.click('button[data-tab="save"]');
  await page.selectOption('.gj-saver-select[data-field="Save to"]', 'memory');
  await page.fill('.gj-saver-name', 'plan');
  await page.click('.gj-saver-save');
  await page.waitForSelector('.gj-saver-entry[data-key="plan.json"]');
  const original = (await d.state()).text;

  // Versions: snapshot, change the text, open the snapshot to go back.
  assert(await page.locator('.gj-saver-versions h4').textContent() === 'Versions of plan', 'the versions of the named document are shown');
  await page.click('.gj-saver-version-save');
  await page.waitForSelector('.gj-saver-version');
  await d.toggleMode();
  await d.click(240, 164);
  await page.focus('#input');
  await page.keyboard.press('End');
  await page.keyboard.type(' CHANGED');
  await page.waitForFunction(() => JSON.parse(document.getElementById('state').textContent).text.includes('CHANGED'));
  await page.click('button[data-tab="save"]');
  await page.click('.gj-saver-version .gj-saver-version-open');
  await page.waitForFunction((t) => JSON.parse(document.getElementById('state').textContent).text === t, original);
  assert(true, 'opening a version brings the text back');
  await page.click('.gj-saver-version .gj-saver-version-delete');
  await page.waitForFunction(() => document.querySelectorAll('.gj-saver-version').length === 0);
  assert(true, 'a version can be deleted');

  // Duplicate and tags.
  await page.click('.gj-saver-entry[data-key="plan.json"] .gj-saver-duplicate');
  await page.waitForSelector('.gj-saver-entry[data-key="Copy of plan.json"]');
  assert(await page.locator('.gj-saver-entry').count() === 2, 'Duplicate saves a copy');
  await page.fill('.gj-saver-entry[data-key="plan.json"] .gj-saver-tags', 'rpg, draft');
  await page.dispatchEvent('.gj-saver-entry[data-key="plan.json"] .gj-saver-tags', 'change');
  await page.waitForSelector('.gj-saver-tagchip:text-is("rpg")');
  assert(await page.locator('.gj-saver-tagchip').count() === 3, 'the tags appear as filters (All, draft, rpg)');
  await page.click('.gj-saver-tagchip:text-is("rpg")');
  await page.waitForFunction(() => document.querySelectorAll('.gj-saver-entry').length === 1);
  assert(await page.locator('.gj-saver-entry[data-key="plan.json"]').count() === 1, 'filtering by a tag leaves only the documents with it');
  await page.click('.gj-saver-tagchip:text-is("All")');
  await page.waitForFunction(() => document.querySelectorAll('.gj-saver-entry').length === 2);

  // Trash.
  await page.click('.gj-saver-entry[data-key="Copy of plan.json"] .gj-saver-trash');
  await page.waitForSelector('.gj-saver-trashed');
  assert(await page.locator('.gj-saver-entry').count() === 1 && await page.locator('.gj-saver-trashed').count() === 1, 'Trash moves it out of the list into the trash');
  await page.click('.gj-saver-trashed .gj-saver-restore');
  await page.waitForFunction(() => document.querySelectorAll('.gj-saver-entry').length === 2 && document.querySelectorAll('.gj-saver-trashed').length === 0);
  assert(true, 'Restore brings it back');
  await page.click('.gj-saver-entry[data-key="Copy of plan.json"] .gj-saver-trash');
  await page.waitForSelector('.gj-saver-trashed');
  await page.click('.gj-saver-trashed .gj-saver-purge');
  await page.waitForFunction(() => document.querySelectorAll('.gj-saver-trashed').length === 0);
  assert(await page.locator('.gj-saver-entry').count() === 1, 'Delete forever removes it for good');
  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
