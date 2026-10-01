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
  assert(colBreak.page === 1 && colBreak.col === 0, 'the column break was in the first column of page 1');
  assert(s.lines.some((l) => l.page === 1 && l.col === 1), 'text after it continues in the next column');
  assert(s.bands.some((b) => b.page === 0 && b.xs.length === 2), 'two columns on page 0 (set by \\columns 2)');
  assert(s.bands.some((b) => b.page === 1 && b.xs.length === 2), 'the column config carries on to page 1 (from a natural break)');
  assert(s.breaks.some((b) => b.kind === 'natural' && b.page === 0 && b.col === 1), 'a natural page break after the last column of page 0');

  // The Markdown transform: formats, an inline image fitted into the page, a table.
  for (const k of ['underline', 'strike', 'super', 'sub', 'background']) {
    assert(s.decorations[k] > 0, 'markdown produced a ' + k + ' run');
  }
  const inlineImg = s.objects.find((o) => o.kind === 'image');
  const [px, py, pw] = s.pageRects[inlineImg.page];
  assert(inlineImg.h <= 107 && inlineImg.x >= px + 50 && inlineImg.x + inlineImg.w <= px + pw - 50, 'the inline image sits inside the page margins');
  assert(s.objects.some((o) => o.kind === 'table' && o.page === 2), 'the markdown table is laid out as a block object');
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

  // Headers, footers and page numbers: double click to edit; per page =
  // previous page or new; the page number is selectable and has settings.
  assert(s.headers[1] === 'Gojiberry Quarterly' && s.headers[2] === 'Gojiberry Quarterly' && s.headers[3] === '', 'headers inherit down the pages (page 3 removed it)');
  await scrollTo(s.pageRects[1][1] - 40);
  const [h1x, h1y] = view(s.pageRects[1][0] + 100, s.pageRects[1][1] + 30);
  await page.mouse.move(...d.at(h1x, h1y));
  await page.mouse.dblclick(...d.at(h1x, h1y));
  await refresh();
  assert(s.hfEdit.editing && s.hfEdit.page === 1 && !s.hfEdit.footer, 'double click on a header starts editing it');
  assert(s.headers[1] === 'Gojiberry Quarterly', 'looking at an inherited header does not change anything');
  await page.keyboard.type(' (draft)');
  await refresh();
  assert(s.headers[0] === 'Gojiberry Quarterly' && s.headers[1] === 'Gojiberry Quarterly (draft)' && s.headers[2] === 'Gojiberry Quarterly (draft)', 'typing makes page 1 its own header, which page 2 inherits: ' + s.headers);
  await page.keyboard.press('Escape');

  // Select page number 2 (the third page) and change its format.
  await scrollTo(s.pageRects[2][1] + s.pageRects[2][3] - 400);
  const nr = s.numberRects[2];
  const [nx, ny] = view(nr[0] + nr[2] / 2, nr[1] + nr[3] / 2);
  await d.click(nx, ny);
  await refresh();
  assert(s.numberSelected.ok && s.numberSelected.page === 2 && s.menu.open, 'clicking a page number selects it and opens its settings menu');
  const labelIdx = s.menu.labels.findIndex((l) => l.trim().endsWith('Page {n} of {total}'));
  assert(labelIdx >= 0, 'the menu offers formats: ' + s.menu.labels.join(' | '));
  // Menu rows: 28px items, 9px separators (labelled "").
  let rowTop = s.menu.rect[1];
  for (let i = 0; i < labelIdx; i++) rowTop += s.menu.labels[i] === '' ? 9 : 28;
  const [pmx, pmy] = view(s.menu.rect[0] + 20, rowTop + 14);
  await d.click(pmx, pmy);
  await refresh();
  assert(s.pageNumbers[2] === 'Page 2 of 4' && s.pageNumbers[1] === 'Page 1' && s.pageNumbers[3] === '— i —', 'the format applies from that page on, earlier pages keep theirs: ' + s.pageNumbers);
  assert(!s.menu.open, 'the menu closes');

  // Image: move (snaps to 5) and the text wraps around it.
  await scrollTo(s.image.visible[1] - 200);
  let im = s.image;
  assert(im.wrap && !intrudes(s.lines.filter((l) => l.page === 1), grow(im.visible)), 'text wraps around the picture');
  const [ix, iy] = view(im.visible[0] + im.visible[2] / 2, im.visible[1] + im.visible[3] / 2);
  await d.drag(ix, iy, ix - 83, iy);
  await refresh();
  im = s.image;
  assert(im.anchor.Page === 1 && im.anchor.X === 245 && im.anchor.Y === 150, 'the anchored picture moved left, snapping to 5 relative to its page: ' + JSON.stringify(im.anchor));
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

  // --- more formatting: text color and highlight from the toolbar palettes ---
  await page.mouse.move(...d.at(...view(f.x + f.w - 14, f.y + 14)));
  await d.click(...view(f.x + f.w - 14, f.y + 14));
  await page.keyboard.down('Shift');
  await page.keyboard.press('Home');
  await page.keyboard.up('Shift');
  await refresh();
  const pick = async (button, index) => {
    const [bx, by, bw, bh] = s.floating.buttons[button];
    await d.click(...view(bx + bw / 2, by + bh / 2));
    await refresh();
    assert(s.floating.palette.open, button + ' button opens its palette: ' + s.floating.palette.labels.join(','));
    const [px2, py2] = s.floating.palette.rect;
    await d.click(...view(px2 + 20, py2 + 28 * index + 14));
    await refresh();
    assert(!s.floating.palette.open && s.floating.editing, 'choosing a swatch closes the palette and keeps editing');
  };
  await pick('color', 2); // Red
  assert(s.floating.items.some((it) => it.Format && it.Format.Color === '#d93025'), 'the selected text is colored red: ' + JSON.stringify(s.floating.items.map((i) => i.Format.Color)));
  await pick('mark', 0); // Yellow
  assert(s.floating.items.some((it) => it.Format && it.Format.Background === '#fff475' && it.Format.Color === '#d93025'), 'and highlighted, keeping the color');

  // --- auto-close pairs ---
  await page.keyboard.press('ArrowRight'); // collapse to the end of the selection
  await page.keyboard.type(' (x');
  await refresh();
  assert(s.floating.text.includes(' (x)') && s.floating.text.split(')').length === 2, 'typing ( inserts the closing ): ' + s.floating.text);
  await page.keyboard.type(')');
  await refresh();
  assert(!s.floating.text.includes('))') && s.floating.text.includes('(x)'), 'typing the closer overtypes it');
  await page.keyboard.type('[');
  await page.keyboard.press('Backspace');
  await refresh();
  assert(!s.floating.text.includes('[') && !s.floating.text.includes(']'), 'Backspace between a pair deletes both');

  // --- auto-complete from the words in the text ---
  await page.keyboard.type(' Flo');
  await refresh();
  assert(s.floating.suggestions.includes('Floating'), 'typing a prefix offers completions: ' + s.floating.suggestions);
  await page.keyboard.press('Tab');
  await refresh();
  assert(s.floating.text.includes('(x) Floating edit') && s.floating.suggestions.length === 0, 'Tab accepts the suggestion: ' + s.floating.text);
  await page.keyboard.type(' Qua');
  await refresh();
  assert(s.floating.suggestions.includes('Quarterly'), 'the Vocabulary words are offered too');
  await page.keyboard.press('Escape');
  await refresh();
  assert(s.floating.suggestions.length === 0 && s.floating.editing, 'Escape dismisses the popup but keeps editing');

  // --- clipboard (through the browser's copy/paste events) ---
  await page.keyboard.press('Control+A');
  await refresh();
  assert(s.floating.selection[1] - s.floating.selection[0] === s.floating.text.length, 'Ctrl+A selects all the text');
  const copied = await page.evaluate(() => {
    const dt = new DataTransfer();
    document.getElementById('input').dispatchEvent(new ClipboardEvent('copy', { clipboardData: dt, bubbles: true, cancelable: true }));
    return dt.getData('text/plain');
  });
  assert(copied === s.floating.text, 'copy puts the selection on the clipboard: ' + copied);
  await page.evaluate(() => {
    const dt = new DataTransfer();
    dt.setData('text/plain', 'PASTED (');
    document.getElementById('input').dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));
  });
  await refresh();
  assert(s.floating.text === 'PASTED (', 'paste replaces the selection, without auto-closing: ' + s.floating.text);
  await page.keyboard.press('Control+A');
  await page.evaluate(() => {
    document.getElementById('input').dispatchEvent(new ClipboardEvent('cut', { clipboardData: new DataTransfer(), bubbles: true, cancelable: true }));
  });
  await refresh();
  assert(s.floating.text === '', 'cut removes the selection');
  await page.keyboard.type('Floating note');

  // --- select text in the document, copy its format, paste it into the note ---
  await scrollTo(0);
  const heading = s.lines.find((l) => l.page === 0);
  await page.mouse.dblclick(...d.at(...view(heading.x + 20, heading.y + 14)));
  await refresh();
  assert(s.flowSelection.text === 'Quarterly', 'double click selects a word of the document: ' + s.flowSelection.text);
  await page.keyboard.press('Control+Alt+C');
  await refresh();
  assert(s.formatClipboard.has && s.formatClipboard.format.Scale === 2 && s.formatClipboard.format.FontWeight === 700, 'Ctrl+Alt+C copies the selection\'s common format: ' + JSON.stringify(s.formatClipboard.format));
  // A drag-selection across differently formatted text copies only what they share.
  const body = s.lines.filter((l) => l.page === 0)[2];
  await d.drag(...view(heading.x + 2, heading.y + 14), ...view(body.x + 150, body.y + 8));
  await refresh();
  assert(s.flowSelection.text.includes('Quarterly Report') && s.flowSelection.text.includes('Markdown'), 'dragging selects across paragraphs');
  await page.keyboard.press('Control+Alt+C');
  await refresh();
  assert(s.formatClipboard.format.Scale === 0 && s.formatClipboard.format.FontWeight === 0, 'a mixed selection has no common size or weight');
  // Back to the heading word, then into the note.
  await page.mouse.dblclick(...d.at(...view(heading.x + 20, heading.y + 14)));
  await page.keyboard.press('Control+Alt+C');
  await scrollTo(s.floating.y - 220);
  await d.click(...view(s.floating.x + s.floating.w - 14, s.floating.y + 14));
  await page.keyboard.press('Control+A');
  await page.keyboard.press('Control+Alt+V');
  await refresh();
  assert(!s.flowSelection.text && s.floating.items.length === 1 && s.floating.items[0].Format.Scale === 2 && s.floating.items[0].Format.FontWeight === 700, 'Ctrl+Alt+V paints the format onto the note: ' + JSON.stringify(s.floating.items));
  await page.keyboard.press('Control+B');
  await refresh();
  assert(s.floating.items[0].Format.FontWeight === 400, 'Ctrl+B toggles bold off (keymap command)');
  await page.keyboard.press('Escape');

  // --- the floating table: text wraps around it; its icon drags it ---
  await scrollTo(s.table.rect[1] - 260);
  let tb = s.table;
  assert(tb.wrap && !intrudes(s.lines.filter((l) => l.page === 3), grow(tb.rect)), 'text wraps around the floating table');
  assert(tb.anchor.Page === 3 && tb.anchor.X === 300, 'the table is anchored to page 3');
  const icon = view(tb.rect[0] - 12, tb.rect[1] - 12);
  await d.drag(...icon, icon[0] - 53, icon[1] + 0);
  await refresh();
  tb = s.table;
  assert(tb.anchor.X === 245 && tb.anchor.Page === 3, 'dragging the table icon moves the table, snapping to 5 relative to its page: ' + JSON.stringify(tb.anchor));
  assert(!intrudes(s.lines.filter((l) => l.page === 3), grow(tb.rect)) && !s.overflow, 'text re-wrapped around the moved table');

  // Move the note by its toolbar's Move button: both axes, snapping.
  await scrollTo(s.floating.y - 220);
  f = s.floating;
  const [mx, my] = view(f.x + 20, f.y - 17);
  await d.drag(mx, my, mx + 22, my + 17);
  await refresh();
  assert(s.floating.x === f.x + 20 && s.floating.y === f.y + 15, `the note moved, snapped to 5: ${f.x},${f.y} -> ${s.floating.x},${s.floating.y}`);

  assert(d.errors.length === 0, 'no page errors: ' + d.errors.join('; '));
  await d.close();
});
