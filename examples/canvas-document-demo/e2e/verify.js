// Drives canvas-document-demo in headless Chromium: the page layout
// (breaks, column configs persisting across pages, headers/footers/page
// numbers), scrolling and clipping, then in edit mode dragging a column
// grip, moving the picture (snapping, with text re-wrapping around it)
// and editing/formatting the floating note.
const { assert, open, run } = require(process.env.E2E_LIB);

const near = (a, b, eps = 1.5) => Math.abs(a - b) <= eps;
// Does any laid-out line on `page` intrude on rect [x, y, w, h]?
const intrudes = (lines, [x, y, w, h]) =>
  lines.some((l) => l.y < y + h && l.y + l.h > y && l.x < x + w && l.x + l.w > x);
const grow = ([x, y, w, h], m = 8) => [x - m, y - m, w + 2 * m, h + 2 * m];

run(async () => {
  const d = await open();
  const { page } = d;
  let s = await d.state();
  const [vx, vy, vw, vh] = s.viewport;

  // Content (document) coordinates -> canvas coordinates, given the scroll.
  const view = (cx, cy) => [vx + cx - s.scroll[0], vy + cy - s.scroll[1]];
  const refresh = async () => { s = await d.state(); };
  const scrollTo = async (y) => {
    await page.mouse.move(...d.at(vx + vw / 2, vy + vh / 2));
    await page.mouse.wheel(0, y - s.scroll[1]);
    await refresh();
  };

  // --- layout ---
  assert(s.pageCount === 4 && s.pagesUsed === 4 && !s.overflow, 'text fills four pages without overflowing');
  const kinds = new Set(s.breaks.map((b) => b.kind));
  assert(kinds.has('natural') && kinds.has('page') && kinds.has('column'), 'natural, user page and user column breaks all occur: ' + [...kinds]);
  const colBreak = s.breaks.find((b) => b.kind === 'column');
  assert(colBreak.page === 0 && colBreak.col === 1, 'the column break was in the last column of page 0');
  assert(s.lines.some((l) => l.page === 1) && s.bands.some((b) => b.page === 0 && b.xs.length === 2), 'two columns on page 0');
  assert(s.bands.some((b) => b.page === 1 && b.xs.length === 2), 'the column config carries on to page 1 (from the last-column break)');
  const pb = s.breaks.find((b) => b.kind === 'page');
  const firstOnNext = s.lines.find((l) => l.page === pb.page + 1);
  assert(near(firstOnNext.y, s.pageRects[pb.page + 1][1] + 60), 'a user page break resumes at the top of the next page text area');
  assert(s.bands.some((b) => b.page === 3 && b.xs.length === 1), 'later pages use the single-column config set after the columns');
  assert(JSON.stringify(s.pageNumbers) === JSON.stringify(['', 'Page 1', 'Page 2', '— i —']), 'per-page page-number configs: ' + s.pageNumbers);

  // --- scrolling and clipping ---
  assert(s.content[1] > vh, 'the document is taller than the viewport');
  assert((await d.pixel(vx + vw + 6, vy + 100))[3] === 0 && (await d.pixel(vx + 100, vy + vh + 6))[3] === 0, 'the pages are clipped to the viewport');
  await scrollTo(450);
  assert(s.scroll[1] === 450, 'wheel scrolls the document');
  const header = await d.pixel(...view(s.pageRects[1][0] + 60, s.pageRects[1][1] + 28));
  assert(header[3] !== 0, 'the scrolled pages are redrawn');

  // --- edit mode ---
  await d.toggleMode();
  await refresh();

  // Drag a column grip on page 0: the config is shared, so page 1's columns follow.
  await scrollTo(150);
  const band0 = s.bands.find((b) => b.page === 0 && b.xs.length === 2);
  const gripX = band0.xs[0] + band0.widths[0] + band0.gap / 2;
  const [gx, gy] = view(gripX, band0.top - 2 - 8);
  await d.drag(gx, gy, gx + 30, gy);
  await refresh();
  const after0 = s.bands.find((b) => b.page === 0 && b.xs.length === 2);
  const after1 = s.bands.find((b) => b.page === 1 && b.xs.length === 2);
  assert(near(after0.widths[0], band0.widths[0] + 30) && near(after0.widths[1], band0.widths[1] - 30), 'dragging the grip resizes the two columns: ' + after0.widths);
  assert(near(after1.widths[0], after0.widths[0]), 'the next page keeps the new column widths');
  assert(!s.overflow && s.pagesUsed === 4, 'the document still fits');

  // Image: move (snaps to 5) and the text wraps around it.
  await scrollTo(s.image.visible[1] - 200);
  let im = s.image;
  assert(im.wrap && !intrudes(s.lines.filter((l) => l.page === 1), grow(im.visible)), 'text wraps around the picture');
  const [ix, iy] = view(im.visible[0] + im.visible[2] / 2, im.visible[1] + im.visible[3] / 2);
  await d.drag(ix, iy, ix - 83, iy);
  await refresh();
  im = s.image;
  assert(im.x === 345 && im.x % 5 === 0, 'the picture moved left and snapped to 5: x=' + im.x);
  assert(!intrudes(s.lines.filter((l) => l.page === 1), grow(im.visible)), 'text re-wrapped around the moved picture');
  // Wrap button (4th in the toolbar) off: text ignores the picture.
  const [wx, wy] = view(im.visible[0] + 3 * 56 + 10, im.visible[1] - 17);
  await d.click(wx, wy);
  await refresh();
  assert(!s.image.wrap && intrudes(s.lines.filter((l) => l.page === 1), s.image.visible), 'with wrap off, text runs under the picture');
  await d.click(wx, wy);
  await refresh();
  assert(s.image.wrap, 'wrap back on');

  // Floating note: edit and format.
  await scrollTo(s.floating.y - 220);
  let f = s.floating;
  assert(f.wrap && f.text.startsWith('Floating note'), 'floating note is there');
  const [fx, fy] = view(f.x + f.w - 14, f.y + 14);
  await d.click(fx, fy); // right of the first line of text: caret at its end
  await refresh();
  assert(s.floating.editing, 'clicking into the note starts editing');
  await page.keyboard.down('Shift');
  await page.keyboard.press('Home');
  await page.keyboard.up('Shift');
  await refresh();
  const [from, to] = s.floating.selection;
  assert(to - from > 5, 'shift+Home selects the line: ' + s.floating.selection);
  const [bx, by] = view(f.x + 44 + 4 + 14, f.y - 17);
  await d.click(bx, by); // B
  await refresh();
  const bold = s.floating.items.filter((it) => it.Format && it.Format.FontWeight === 700);
  assert(bold.length === 1 && bold[0].Text.length === to - from, 'B bolds exactly the selection: ' + JSON.stringify(s.floating.items));
  assert(s.floating.editing && s.floating.selection[1] - s.floating.selection[0] === to - from, 'the selection survives the toolbar click');
  await page.keyboard.press('End');
  await page.keyboard.type('!');
  await refresh();
  assert(s.floating.text.includes('!'), 'typing inserts text: ' + s.floating.text);
  await page.keyboard.press('Escape');
  await refresh();
  assert(!s.floating.editing, 'Escape stops editing');

  // Move the note by its toolbar's Move button: both axes, snapping.
  f = s.floating;
  const [mx, my] = view(f.x + 20, f.y - 17);
  await d.drag(mx, my, mx + 22, my + 17);
  await refresh();
  assert(s.floating.x === f.x + 20 && s.floating.y === f.y + 15, `the note moved, snapped to 5: ${f.x},${f.y} -> ${s.floating.x},${s.floating.y}`);

  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
