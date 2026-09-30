// Shared helpers for the canvas examples' Playwright e2e scripts.
const { chromium } = require('playwright');

function assert(cond, msg) {
  if (!cond) throw new Error('FAIL: ' + msg);
  console.log('ok:', msg);
}

// open launches headless Chromium on the example and returns helpers.
// The demos write a JSON summary of their state into #state after every
// change, since a canvas has no DOM to read back.
async function open() {
  const browser = await chromium.launch({
    executablePath: process.env.PLAYWRIGHT_CHROMIUM || '/opt/pw-browsers/chromium',
    args: ['--no-sandbox'],
  });
  const page = await browser.newPage({ viewport: { width: 1000, height: 800 } });
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  await page.goto(`http://localhost:${process.env.PORT}/index.html`);
  await page.waitForFunction(() => document.getElementById('state') && document.getElementById('state').textContent !== '(pending)', null, { timeout: 15000 });
  const box = await page.locator('#stage').boundingBox();
  return {
    browser, page, errors, box,
    state: async () => JSON.parse(await page.locator('#state').textContent()),
    // Canvas-local coordinates -> page coordinates.
    at: (x, y) => [box.x + x, box.y + y],
    click: async (x, y) => { await page.mouse.move(box.x + x, box.y + y); await page.mouse.click(box.x + x, box.y + y); },
    drag: async (x0, y0, x1, y1) => {
      await page.mouse.move(box.x + x0, box.y + y0);
      await page.mouse.down();
      await page.mouse.move(box.x + (x0 + x1) / 2, box.y + (y0 + y1) / 2, { steps: 3 });
      await page.mouse.move(box.x + x1, box.y + y1, { steps: 3 });
      await page.mouse.up();
    },
    // Canvas pixel [r,g,b,a] at canvas-local (x, y).
    pixel: (x, y) => page.evaluate(([x, y]) => {
      const c = document.getElementById('stage');
      return Array.from(c.getContext('2d').getImageData(x, y, 1, 1).data);
    }, [x, y]),
    toggleMode: () => page.click('#mode'),
    close: async () => { await browser.close(); },
  };
}

function run(fn) {
  fn().then(() => console.log('ALL CHECKS PASSED'), (e) => { console.error(e.message || e); process.exit(1); });
}

module.exports = { assert, open, run };
