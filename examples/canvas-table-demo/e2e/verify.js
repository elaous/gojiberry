// Drives canvas-table-demo in headless Chromium: view mode is read-only;
// edit mode lets you type, Tab between cells, add columns/rows with the
// "+" bars, and use the column and table handle menus. Asserts on the
// JSON the demo mirrors into #state, plus a few canvas pixels.
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const d = await open();
  const { page } = d;
  const mid = (edges, i) => (edges[i] + edges[i + 1]) / 2;

  let s = await d.state();
  assert(s.mode === 'view', 'starts in view mode');
  assert(s.cells.length === 3 && s.cells[0].join() === 'Name,Role,Status', 'initial cells');

  // View mode: clicks and typing do nothing.
  await d.click(50, 50);
  await page.keyboard.type('zzz');
  s = await d.state();
  assert(s.active === null && s.cells[0][0] === 'Name', 'view mode ignores clicks and typing');

  await d.toggleMode();
  s = await d.state();
  assert(s.mode === 'edit', 'toggle enters edit mode');

  // Type into a cell, then Tab on.
  await d.click(50, 50);
  await page.keyboard.press('End');
  await page.keyboard.type('!');
  s = await d.state();
  assert(s.cells[0][0] === 'Name!' && s.active.join() === '0,0', 'typing edits the active cell');
  await page.keyboard.press('Tab');
  s = await d.state();
  assert(s.active.join() === '0,1', 'Tab moves to the next cell');
  await page.keyboard.press('Escape');

  // "+" bars.
  s = await d.state();
  const [w, h] = s.size;
  await d.click(w - 11, mid(s.rowEdges, 0) + 30); // add-column bar
  s = await d.state();
  assert(s.cells[0].length === 4 && s.active.join() === '0,3', '"+" on the right adds a column');
  await d.click(40, s.size[1] - 11); // add-row bar
  s = await d.state();
  assert(s.cells.length === 4 && s.active.join() === '3,0', '"+" at the bottom adds a row');

  // Column handle: selects the column and opens a menu.
  const colX = mid(s.colEdges, 1);
  await d.click(colX, 24);
  s = await d.state();
  assert(s.selection.kind === 'column' && s.selection.index === 1 && s.menuOpen, 'column grip selects it and opens the menu');
  const grip = await d.pixel(Math.round(colX) - 10, 24); // the pill, left of its dots
  assert(grip[0] === 0x7c && grip[1] === 0x5c && grip[2] === 0xff, 'selected column grip is drawn in the accent color');

  // "Insert column left" is the menu's first item.
  await d.click(s.colEdges[1] + 20, 32 + 14);
  s = await d.state();
  assert(s.cells[0].length === 5 && s.cells[0][1] === '' && s.cells[0][2] === 'Role', 'menu: insert column left');
  assert(!s.menuOpen, 'menu closes after choosing');

  // Delete that column again: re-open and pick "Delete column" (last item).
  await d.click(mid(s.colEdges, 1), 24);
  s = await d.state();
  await d.click(s.colEdges[1] + 20, 32 + 28 * 6 + 9 * 3 + 14);
  s = await d.state();
  assert(s.cells[0].length === 4 && s.cells[0][1] === 'Role', 'menu: delete column');

  // Table handle -> Clear contents.
  await d.click(24, 24);
  s = await d.state();
  assert(s.selection.kind === 'table' && s.menuOpen, 'corner icon selects the table');
  await d.click(40, 32 + 28 * 2 + 9 + 14);
  s = await d.state();
  assert(s.cells.every((r) => r.every((c) => c === '')), 'menu: clear contents');

  // Back to view mode hides the editing UI.
  await d.toggleMode();
  s = await d.state();
  assert(s.mode === 'view' && s.selection.kind === 'none' && !s.menuOpen, 'view mode drops selection and menu');

  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
