// Drives canvas-image-demo in headless Chromium: move (snapping to 5px),
// clip, alpha and text wrap, and that view mode is read-only. Asserts on
// #state and on real canvas pixels.
const { assert, open, run } = require(process.env.E2E_LIB);

// intrudes: does any laid-out text overlap the rect (x, y, w, h)?
function intrudes(lines, lineH, [x, y, w, h]) {
  return lines.some((l) => l.Y < y + h && l.Y + lineH > y && l.X < x + w && l.X + l.W > x);
}
const margin = 8;
const grow = ([x, y, w, h]) => [x - margin, y - margin, w + 2 * margin, h + 2 * margin];

run(async () => {
  const d = await open();
  let s = await d.state();
  assert(s.mode === 'view' && !s.wrap && s.alpha === 1 && s.x === 60, 'starts in view mode, opaque, not wrapping');
  assert(intrudes(s.lines, 22, s.visible), 'without wrap, text runs underneath the picture');

  // View mode: dragging the picture does nothing.
  await d.drag(150, 190, 200, 190);
  s = await d.state();
  assert(s.x === 60, 'view mode ignores drags');

  await d.toggleMode();

  // Toolbar is 28px above the picture: Move, Clip, Alpha, Wrap, 56px apart.
  const btn = (i) => [s.visible[0] + 56 * i + 10, s.visible[1] - 17];

  // Move: drag right 33px -> position snaps to a multiple of 5.
  await d.drag(150, 190, 183, 190);
  s = await d.state();
  assert(s.x === 95 && s.x % 5 === 0 && s.y === 110, 'drag moves left/right and snaps to 5: x=' + s.x);
  await d.drag(s.x + 50, 190, s.x + 50 + 12, 260);
  s = await d.state();
  assert(s.x === 105 && s.y === 110, 'vertical motion is ignored, 12px snaps to 10: x=' + s.x);

  // Wrap on: text leaves the picture's rectangle (plus margin) alone.
  await d.click(...btn(3));
  s = await d.state();
  assert(s.wrap, 'Wrap button turns wrapping on');
  assert(!intrudes(s.lines, 22, grow(s.visible)), 'text no longer overlaps the picture');
  const wrappedLines = s.lines.length;

  // Dragging the picture re-wraps live.
  await d.drag(s.x + 100, 190, s.x + 100 + 200, 190);
  s = await d.state();
  assert(s.x === 305, 'picture moved again: ' + s.x);
  assert(!intrudes(s.lines, 22, grow(s.visible)), 'text still clear of the picture after moving it');

  // Clip: drag the left handle right.
  await d.click(...btn(1));
  s = await d.state();
  assert(s.tool === 'clip', 'Clip button selects the clip tool');
  const [vx, vy, vw, vh] = s.visible;
  await d.drag(vx, vy + vh / 2, vx + 47, vy + vh / 2);
  s = await d.state();
  assert(s.clip.Left === 45 && s.visible[0] === vx + 45 && s.visible[2] === vw - 45, 'left edge clipped, snapped to 5: ' + JSON.stringify(s.clip));
  assert(!intrudes(s.lines, 22, grow(s.visible)), 'text re-wraps around the cropped picture');
  await d.drag(s.visible[0] + s.visible[2], s.visible[1] + s.visible[3] / 2, s.visible[0] + s.visible[2] - 92, s.visible[1] + s.visible[3] / 2);
  s = await d.state();
  assert(s.clip.Right === 90, 'right edge clipped: ' + JSON.stringify(s.clip));

  // Alpha: slider sets opacity in 5% steps; the picture gets lighter.
  const probe = [s.visible[0] + s.visible[2] - 10, s.visible[1] + s.visible[3] - 10];
  await d.click(...btn(2));
  s = await d.state();
  const before = await d.pixel(...probe);
  await d.click(s.visible[0] + s.visible[2] / 2, s.visible[1] + s.visible[3] + 14);
  s = await d.state();
  assert(s.tool === 'alpha' && Math.abs(s.alpha - 0.5) < 0.051 && Math.round(s.alpha * 20) === s.alpha * 20, 'slider sets alpha in 5% steps: ' + s.alpha);
  const after = await d.pixel(...probe);
  assert(after[0] + after[1] + after[2] > before[0] + before[1] + before[2], 'half-transparent picture blends toward the page: ' + before + ' -> ' + after);

  // Wrap off: text goes back to ignoring the picture.
  await d.click(...btn(3));
  s = await d.state();
  assert(!s.wrap && intrudes(s.lines, 22, s.visible), 'Wrap off lets text run under the picture again');

  // Back to view mode: state is kept but nothing is editable.
  await d.toggleMode();
  const x0 = (await d.state()).x;
  await d.drag(s.visible[0] + 20, s.visible[1] + 20, s.visible[0] + 120, s.visible[1] + 20);
  s = await d.state();
  assert(s.mode === 'view' && s.x === x0, 'view mode is read-only again');

  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
