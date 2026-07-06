// Headless-browser screenshot of the built demo, for verifying it
// actually mounts and renders (not just that it builds). Needs a
// globally installed playwright + chromium; point PLAYWRIGHT_CHROMIUM
// at the executable if it's not on the default path this was written
// against.
const { chromium } = require('playwright');

const port = process.env.PORT || '8080';
const chromiumPath = process.env.PLAYWRIGHT_CHROMIUM || '/opt/pw-browsers/chromium-1194/chrome-linux/chrome';
const outFile = process.env.OUT_FILE || 'screenshot.png';

(async () => {
  const browser = await chromium.launch({ executablePath: chromiumPath, args: ['--no-sandbox'] });
  const page = await browser.newPage({ viewport: { width: 1400, height: 1000 } });
  const errors = [];
  page.on('console', msg => { if (msg.type() === 'error') errors.push(msg.text()); });
  page.on('pageerror', err => errors.push(String(err)));

  await page.goto(`http://localhost:${port}/index.html`, { waitUntil: 'networkidle' });
  await page.waitForTimeout(1500);

  await page.screenshot({ path: outFile, fullPage: true });

  if (errors.length) console.error('console errors:', errors);
  await browser.close();
})();
