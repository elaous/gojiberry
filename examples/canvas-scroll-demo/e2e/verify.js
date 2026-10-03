// Drives canvas-scroll-demo in headless Chromium: wheel and scrollbar
// scrolling, clipping to the viewport, the vertical/horizontal locks,
// and that a table inside a scrolled view still receives correctly
// mapped clicks in edit mode.
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const d = await open();
  const { page } = d;
  let s = await d.state();
  const [vx, vy, vw, vh] = s.viewport;
  const inside = [vx + 200, vy + 150];

  assert(s.scrollX === 0 && s.scrollY === 0 && s.content[0] > vw && s.content[1] > vh, 'content is bigger than the viewport');
  const alphaOutside = async (x, y) => (await d.pixel(x, y))[3];
  assert(await alphaOutside(vx + vw + 8, vy + 50) === 0 && await alphaOutside(vx + 50, vy + vh + 8) === 0, 'content is clipped to the viewport (nothing painted outside it)');
  assert(await alphaOutside(vx + 100, vy + 100) !== 0, 'content is painted inside it');

  // Wheel scrolls both axes.
  await page.mouse.move(...d.at(...inside));
  await page.mouse.wheel(0, 120);
  s = await d.state();
  assert(s.scrollY === 120 && s.scrollX === 0, 'wheel scrolls vertically: ' + s.scrollY);
  await page.mouse.wheel(90, 0);
  s = await d.state();
  assert(s.scrollX === 90, 'wheel scrolls horizontally: ' + s.scrollX);
  const px = await d.pixel(vx + 5, vy + 5);
  assert(px[3] !== 0, 'the scrolled content redraws');

  // Wheel outside the viewport does nothing to it.
  await page.mouse.move(...d.at(vx + vw + 30, vy + vh + 15));
  await page.mouse.wheel(0, 100);
  s = await d.state();
  assert(s.scrollY === 120, 'wheel outside the viewport is ignored');
  await page.mouse.move(...d.at(...inside));

  // Clamped at the end.
  await page.mouse.wheel(0, 100000);
  s = await d.state();
  assert(s.scrollY === s.content[1] - vh, 'vertical scroll clamps at the end: ' + s.scrollY);
  await page.mouse.wheel(0, -100000);
  await page.mouse.wheel(-100000, 0);
  s = await d.state();
  assert(s.scrollX === 0 && s.scrollY === 0, 'and at the start');

  // Locks.
  await page.check('#lockv');
  await page.mouse.move(...d.at(...inside));
  await page.mouse.wheel(60, 60);
  s = await d.state();
  assert(s.lockV && s.scrollY === 0 && s.scrollX === 60, 'vertical lock: only horizontal moved');
  await page.uncheck('#lockv');
  await page.check('#lockh');
  await page.mouse.move(...d.at(...inside)); // clicking a checkbox moved the pointer
  await page.mouse.wheel(60, 60);
  s = await d.state();
  assert(s.lockH && s.scrollX === 60 && s.scrollY === 60, 'horizontal lock: only vertical moved');
  await page.uncheck('#lockh');
  await page.mouse.move(...d.at(...inside));
  await page.mouse.wheel(-1000, -1000);

  // Drag the vertical scrollbar thumb (right edge, top of the track).
  const thumbX = vx + vw - 5;
  await d.drag(thumbX, vy + 8, thumbX, vy + 8 + 100);
  s = await d.state();
  assert(s.scrollY > 100, 'dragging the scrollbar thumb scrolls: ' + s.scrollY);
  const bar = await d.pixel(thumbX, vy + 8 + 100);
  assert(bar[3] !== 0, 'the thumb is drawn');

  // Edit mode inside a scrolled view: click a cell and check it maps correctly.
  await page.mouse.wheel(-100000, -100000);
  await d.toggleMode();
  await page.mouse.wheel(0, 200);
  await page.mouse.wheel(150, 0);
  s = await d.state();
  const cx = vx + 120, cy = vy + 120; // viewport point
  const contentX = cx - vx + s.scrollX, contentY = cy - vy + s.scrollY;
  const col = s.colEdges.findIndex((e, i) => contentX >= e && contentX < s.colEdges[i + 1]);
  const row = s.rowEdges.findIndex((e, i) => contentY >= e && contentY < s.rowEdges[i + 1]);
  await d.click(cx, cy);
  s = await d.state();
  assert(s.active && s.active[0] === row && s.active[1] === col, `click maps through the scroll offset: expected cell ${row},${col}, got ${s.active}`);
  await page.keyboard.type('Z');
  s = await d.state();
  assert(s.activeText.includes('Z'), 'typing still works inside the view: ' + s.activeText);

  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
