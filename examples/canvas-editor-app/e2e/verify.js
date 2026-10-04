// verify.js drives the canvas-editor-app demo through a real headless
// Chromium (via Playwright) and asserts on #doc-state, the
// machine-readable summary main.xg's updateDebugState writes after
// every redraw -- there's no DOM text to read back from a
// canvas-rendered document otherwise. Run via canvas_editor_test.go
// (`go test`), which starts the HTTP server and passes its port in
// PORT; see that file for how this gets invoked and skipped when
// node/playwright/chromium aren't available.
const { chromium } = require('playwright');

const port = process.env.PORT;
const chromiumPath = process.env.PLAYWRIGHT_CHROMIUM || '/opt/pw-browsers/chromium';

function assert(cond, msg) {
  if (!cond) throw new Error('FAIL: ' + msg);
  console.log('ok:', msg);
}

async function docState(page) {
  return await page.locator('#doc-state').textContent();
}

// field extracts one space-delimited key=value pair out of #doc-state.
// "text" is special-cased since the document's own text legitimately
// contains spaces and newlines, so it can't be delimited the same way
// as the fields after it -- everything up to the next known field
// (" cursor=") belongs to it instead.
function field(state, name) {
  if (name === 'text') {
    const m = state.match(/^text=([\s\S]*) cursor=/);
    return m ? m[1] : undefined;
  }
  const m = state.match(new RegExp(name + '=([^ ]*)'));
  return m ? m[1] : undefined;
}

(async () => {
  const browser = await chromium.launch({ executablePath: chromiumPath, args: ['--no-sandbox'] });
  const page = await browser.newPage({ viewport: { width: 900, height: 700 } });
  const errors = [];
  page.on('console', msg => { if (msg.type() === 'error') errors.push(msg.text()); });
  page.on('pageerror', err => errors.push(String(err)));

  await page.goto(`http://localhost:${port}/index.html`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(800);

  const canvas = page.locator('#editor-canvas');
  const box = await canvas.boundingBox();

  // --- Regression: click positions the cursor, typing inserts there ---
  await page.mouse.click(box.x + 10, box.y + 10);
  await page.keyboard.press('Control+a');
  await page.keyboard.press('Backspace');
  let state = await docState(page);
  assert(field(state, 'text') === '', 'select-all + backspace clears the document');

  await page.keyboard.type('hello');
  state = await docState(page);
  assert(field(state, 'text') === 'hello', 'typing after a click inserts at the cursor');

  // --- Regression: drag-select + Bold toolbar button ---
  await page.mouse.move(box.x + 10, box.y + 20);
  await page.mouse.down();
  await page.mouse.move(box.x + 50, box.y + 20);
  await page.mouse.up();
  state = await docState(page);
  assert(field(state, 'sel') !== '0-0' && field(state, 'sel') !== undefined, 'drag produced a non-empty selection, got sel=' + field(state, 'sel'));
  await page.click('#toolbar-bold');
  state = await docState(page);
  assert(field(state, 'selBold') === 'true', 'clicking Bold toggled the dragged selection bold');

  // Clicking the toolbar button moved focus off the hidden input;
  // re-focus it via the canvas's own mousedown handler before any more
  // keyboard-driven assertions, or every keypress below goes nowhere.
  await page.mouse.click(box.x + 10, box.y + 10);

  // --- Move line up/down ---
  await page.keyboard.press('Control+a');
  await page.keyboard.press('Backspace');
  await page.keyboard.type('line one\nline two\nline three');
  state = await docState(page);
  assert(field(state, 'text') === 'line one\nline two\nline three', 'typed three lines, got: ' + JSON.stringify(field(state, 'text')));

  await page.keyboard.press('Home'); // cursor was at the end (line three); Home -> start of line three
  await page.keyboard.press('Alt+ArrowUp');
  state = await docState(page);
  let text = field(state, 'text');
  assert(text === 'line one\nline three\nline two', 'Alt+ArrowUp swapped line three above line two, got: ' + JSON.stringify(text));

  await page.keyboard.press('Alt+ArrowDown');
  state = await docState(page);
  text = field(state, 'text');
  assert(text === 'line one\nline two\nline three', 'Alt+ArrowDown swapped it back, got: ' + JSON.stringify(text));

  // --- Duplicate line ---
  await page.keyboard.press('Home'); // still on "line three"
  await page.keyboard.press('Shift+Alt+ArrowDown');
  state = await docState(page);
  text = field(state, 'text');
  assert(text === 'line one\nline two\nline three\nline three', 'Shift+Alt+ArrowDown duplicated the current line, got: ' + JSON.stringify(text));

  // --- Word boundary selection + case transform ---
  await page.keyboard.press('Control+a');
  await page.keyboard.press('Backspace');
  await page.keyboard.type('hello world');
  await page.keyboard.press('Home');
  await page.keyboard.press('Control+Shift+ArrowRight');
  state = await docState(page);
  assert(field(state, 'sel') === '0-5', 'Ctrl+Shift+ArrowRight selected exactly "hello" (0-5), got sel=' + field(state, 'sel'));

  await page.keyboard.press('Control+Shift+U');
  state = await docState(page);
  text = field(state, 'text');
  assert(text === 'HELLO world', 'Ctrl+Shift+U uppercased the selection, got: ' + JSON.stringify(text));

  await page.keyboard.press('Control+Shift+L');
  state = await docState(page);
  text = field(state, 'text');
  assert(text === 'hello world', 'Ctrl+Shift+L lowercased it back, got: ' + JSON.stringify(text));

  await page.keyboard.press('End');
  await page.keyboard.press('Control+Shift+ArrowLeft');
  state = await docState(page);
  assert(field(state, 'sel') === '6-11', 'Ctrl+Shift+ArrowLeft from end selected "world" (6-11), got sel=' + field(state, 'sel'));

  // --- Scrolling: auto-follow on typing, manual wheel-scroll, clamping ---
  await page.keyboard.press('Control+a');
  await page.keyboard.press('Backspace');
  const longLines = [];
  for (let i = 0; i < 60; i++) longLines.push('line number ' + i);
  await page.keyboard.type(longLines.join('\n'));
  state = await docState(page);
  const scrollAfterTyping = parseInt(field(state, 'scrollY'), 10);
  assert(scrollAfterTyping > 0, 'typing past the viewport auto-scrolled (scrollY=' + scrollAfterTyping + ' > 0)');

  await canvas.hover();
  await page.mouse.wheel(0, -100000); // an unreasonably large delta -- clampScroll should still floor it at 0
  state = await docState(page);
  assert(field(state, 'scrollY') === '0', 'wheel-scrolling far up clamps to scrollY=0, got ' + field(state, 'scrollY'));

  await page.mouse.wheel(0, 100000); // and ceil it at the document's actual max scroll
  state = await docState(page);
  const scrollAtBottom = parseInt(field(state, 'scrollY'), 10);
  assert(scrollAtBottom > scrollAfterTyping - 50, 'wheel-scrolling far down clamps near the bottom, got scrollY=' + scrollAtBottom);

  // --- Multi-cursor: Alt+Click adds a cursor, typing fans out to all
  // of them, Escape collapses back to one ---
  await page.keyboard.press('Control+a');
  await page.keyboard.press('Backspace');
  await page.keyboard.type('aaaaaaaaaa');

  await page.mouse.click(box.x + 10, box.y + 10);
  state = await docState(page);
  assert(field(state, 'cursors').split(',').length === 1, 'plain click leaves a single cursor, got cursors=' + field(state, 'cursors'));

  await page.keyboard.down('Alt');
  await page.mouse.click(box.x + 60, box.y + 10);
  await page.keyboard.up('Alt');
  state = await docState(page);
  const cursorsAfterAltClick = field(state, 'cursors').split(',');
  assert(cursorsAfterAltClick.length === 2, 'Alt+Click added a second cursor, got cursors=' + field(state, 'cursors'));

  const lenBeforeMultiType = field(state, 'text').length;
  await page.keyboard.type('X');
  state = await docState(page);
  text = field(state, 'text');
  assert(text.length === lenBeforeMultiType + 2, 'typing "X" inserted at both cursors, got: ' + JSON.stringify(text));
  assert((text.match(/X/g) || []).length === 2, 'exactly two X characters were inserted, got: ' + JSON.stringify(text));
  assert(field(state, 'cursors').split(',').length === 2, 'both cursors are still live after typing, got cursors=' + field(state, 'cursors'));

  await page.keyboard.press('Escape');
  state = await docState(page);
  assert(field(state, 'cursors').split(',').length === 1, 'Escape collapsed back to a single cursor, got cursors=' + field(state, 'cursors'));

  const lenBeforeSingleType = field(state, 'text').length;
  await page.keyboard.type('Y');
  state = await docState(page);
  text = field(state, 'text');
  assert(text.length === lenBeforeSingleType + 1, 'typing "Y" after Escape inserted only once, got: ' + JSON.stringify(text));
  assert((text.match(/Y/g) || []).length === 1, 'exactly one Y character was inserted, got: ' + JSON.stringify(text));

  // Chromium's automatic favicon.ico request 404s (this demo serves
  // none) and gets logged as a generic console error with no URL in
  // its text -- filtered out by that exact generic message rather than
  // by URL, since it's the only thing that produces this message; a
  // real bug would throw a distinct JS error instead.
  const realErrors = errors.filter(e => !e.includes('the server responded with a status of 404'));
  if (realErrors.length) {
    console.error('console/page errors encountered:', realErrors);
    process.exitCode = 1;
  } else {
    console.log('\nALL CHECKS PASSED');
  }
  await browser.close();
})().catch(e => { console.error(e); process.exit(1); });
