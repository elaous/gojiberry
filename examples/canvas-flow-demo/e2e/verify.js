// Drives canvas-flow-demo in headless Chromium: font metrics change the
// layout, columns flow text across, and in edit mode the gap grip can be
// dragged to resize neighbouring columns (view mode can't).
const { assert, open, run } = require(process.env.E2E_LIB);

const sum = (a) => a.reduce((x, y) => x + y, 0);

run(async () => {
  const d = await open();
  const { page } = d;

  let s = await d.state();
  assert(s.mode === 'view' && s.columns.length === 1, 'starts in view mode with one column');
  assert(s.lines.length > 3 && !s.overflow, 'text is laid out and fits');
  const ink = await page.evaluate(() => {
    const c = document.getElementById('stage');
    const px = c.getContext('2d').getImageData(30, 40, 840, 60).data;
    let dark = 0;
    for (let i = 0; i < px.length; i += 4) if (px[i] < 100 && px[i + 1] < 100) dark++;
    return dark;
  });
  assert(ink > 200, 'text is actually painted on the canvas (' + ink + ' dark pixels)');

  // Font metrics feed the layout.
  const linesBefore = s.lines.length;
  await page.selectOption('#weight', '700');
  await page.selectOption('#style', 'italic');
  await page.selectOption('#family', 'monospace');
  await page.fill('#size', '24');
  await page.dispatchEvent('#size', 'change');
  s = await d.state();
  assert(s.font === 'italic 700 24px monospace', 'font string reflects family/size/weight/style: ' + s.font);
  assert(s.lines.length > linesBefore, 'bigger text wraps onto more lines');
  await page.fill('#size', '16');
  await page.dispatchEvent('#size', 'change');
  await page.fill('#leading', '40');
  await page.dispatchEvent('#leading', 'change');
  s = await d.state();
  assert(Math.abs(s.lines[1].Y - s.lines[0].Y - 40) < 1e-6, 'line height is honoured');
  await page.selectOption('#weight', '400');
  await page.selectOption('#style', 'normal');
  await page.selectOption('#family', 'sans-serif');
  await page.fill('#leading', '22');
  await page.dispatchEvent('#leading', 'change');

  // Columns.
  await page.selectOption('#cols', '3');
  s = await d.state();
  assert(s.columns.length === 3 && Math.abs(sum(s.columns) + 2 * s.gap - 840) < 1e-6, 'three equal columns fill the page');
  assert(new Set(s.lines.map((l) => l.Col)).size > 1, 'text flows into later columns');
  const gapCenter = 30 + s.columns[0] + s.gap / 2;

  // View mode: the grip isn't there.
  await d.drag(gapCenter, 30, gapCenter + 40, 30);
  s = await d.state();
  assert(Math.abs(s.columns[0] - s.columns[1]) < 1e-6, 'view mode ignores gap drags');

  await d.toggleMode();
  const widths = s.columns.slice();
  await d.drag(gapCenter, 30, gapCenter + 40, 30);
  s = await d.state();
  assert(Math.abs(s.columns[0] - (widths[0] + 40)) < 1.5 && Math.abs(s.columns[1] - (widths[1] - 40)) < 1.5, 'dragging the grip resizes the two neighbours: ' + s.columns.join());
  assert(Math.abs(s.columns[0] + s.columns[1] - widths[0] - widths[1]) < 1e-6 && s.columns[2] === widths[2], 'combined width and the third column are unchanged');
  // The 1px guide straddles two pixel columns, so it is anti-aliased.
  const guide = await d.pixel(gapCenter + 40, 200);
  assert(guide[0] < 250 && guide[2] > guide[0], 'the gap guide line is drawn in edit mode: ' + guide);

  await d.drag(gapCenter + 40, 30, gapCenter + 2000, 30);
  s = await d.state();
  assert(s.columns[1] === 40, 'dragging far right stops at the minimum column width');

  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
