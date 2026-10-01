// Drives canvas-gox-demo: the gox tree renders, its pages template gives
// per-page headers and numbers, state changes re-render in place, and a
// header edited on the canvas survives a re-render.
const { assert, open, run } = require(process.env.E2E_LIB);

run(async () => {
  const d = await open();
  const { page } = d;

  let s = await d.state();
  assert(!s.error, 'renders without error');
  assert(s.pages === 3, 'three pages from the tree');
  assert(JSON.stringify(s.headers) === JSON.stringify(['Gojiberry report', 'Gojiberry report', 'Appendix']),
    'header inherits then changes on page 3: ' + s.headers);
  assert(JSON.stringify(s.numbers) === JSON.stringify(['', 'Page 2 of 3', 'Page 3 of 3']),
    'page 1 has no number, later ones continue: ' + s.numbers);
  const ink = await page.evaluate(() => {
    const px = document.getElementById('stage').getContext('2d').getImageData(170, 80, 560, 200).data;
    let dark = 0;
    for (let i = 0; i < px.length; i += 4) if (px[i] < 100 && px[i + 1] < 100) dark++;
    return dark;
  });
  assert(ink > 200, 'text is painted on the canvas (' + ink + ' dark pixels)');

  // Re-render with a new page count.
  await page.fill('#pages', '4');
  await page.dispatchEvent('#pages', 'change');
  s = await d.state();
  assert(s.pages === 4 && s.numbers[3] === 'Page 4 of 4', 'page count prop adds a sheet: ' + s.numbers);

  // Edit a header on the canvas, then re-render: the edit stays.
  await d.toggleMode();
  s = await d.state();
  assert(s.mode === 'edit', 'mode prop switches the components to edit');
  await page.mouse.dblclick(...d.at(450, 24 + 30));
  await page.keyboard.type('!!');
  s = await d.state();
  assert(s.headers[0].endsWith('!!'), 'header edited on the canvas: ' + s.headers[0]);
  await page.fill('#source', '# Changed\n\nShort text.');
  s = await d.state();
  assert(s.headers[0].endsWith('!!'), 'edited header survives a re-render with new text');
  assert(s.lines < 10, 'new source is laid out (' + s.lines + ' lines)');
  assert(d.errors.length === 0, 'no page errors: ' + d.errors);
  await d.close();
});
