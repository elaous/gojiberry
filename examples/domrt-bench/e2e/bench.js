// bench.js drives one benchmark scenario against the wasm harness
// main.gox mounts: load the page, wait for window.runBench to exist,
// call it once with the scenario/iteration-count bench_test.go passes
// in via env vars, and print the elapsed nanoseconds it returns as
// the last line of stdout (RESULT:<float>) for bench_test.go to parse
// back out. Mirrors every other gojiberry example's own e2e/verify.js
// (see canvas-editor-app's own doc comment) -- just reading back one
// number instead of asserting DOM state.
const { chromium } = require('playwright');

(async () => {
  const port = process.env.PORT;
  const chromiumPath = process.env.PLAYWRIGHT_CHROMIUM;
  const scenario = process.env.BENCH_SCENARIO;
  const n = parseInt(process.env.BENCH_N, 10);

  const browser = await chromium.launch({ executablePath: chromiumPath, args: ['--no-sandbox'] });
  const page = await browser.newPage();
  const errors = [];
  page.on('pageerror', err => errors.push(String(err)));
  page.on('console', msg => { if (msg.type() === 'error') errors.push(msg.text()); });

  await page.goto(`http://localhost:${port}/index.html`, { waitUntil: 'networkidle' });
  await page.waitForFunction(() => typeof window.runBench === 'function');

  const result = await page.evaluate(([scenario, n]) => window.runBench(scenario, n), [scenario, n]);
  await browser.close();

  // A missing favicon (this harness serves no favicon.ico at all) is
  // expected degradation, not a real failure -- same filter OnDesign's
  // own e2e/testkit.ts report() applies.
  const realErrors = errors.filter(e => !e.includes('Failed to load resource'));
  if (realErrors.length) {
    console.error('page errors:', errors);
    process.exit(1);
  }
  if (typeof result !== 'number') {
    console.error('runBench error:', JSON.stringify(result));
    process.exit(1);
  }
  console.log('RESULT:' + result);
})().catch(err => {
  console.error(err);
  process.exit(1);
});
