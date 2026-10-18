// node --test wasmserve/sw-push.test.js
// Runs the push handlers of the generated service worker (the template in sw.go) with a stand-in for the worker's
// global scope: what the worker shows for a message, and where a click goes.
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');

const src = fs.readFileSync(path.join(__dirname, 'sw.go'), 'utf8');
const template = src.slice(src.indexOf('const swTemplate = `') + 'const swTemplate = `'.length, src.lastIndexOf('`'));

function worker() {
  const listeners = {};
  const shown = [], opened = [], focused = [];
  let windows = [];
  const self = {
    registration: { scope: 'https://app.example/editor/', showNotification: (title, options) => { shown.push({ title, options }); return Promise.resolve(); } },
    addEventListener: (type, fn) => { listeners[type] = fn; },
    clients: {
      matchAll: () => Promise.resolve(windows),
      openWindow: (url) => { opened.push(url); return Promise.resolve(); },
      claim: () => Promise.resolve(),
    },
    skipWaiting: () => Promise.resolve(),
  };
  const code = template.replace('__VERSION__', 'v').replace('__FILES__', '[]').replace('__LAZY__', '[]');
  vm.runInNewContext(code, { self, caches: {}, fetch: () => {}, Request: class {}, Response: class {}, URL, console });
  const run = async (type, event) => {
    let waited = Promise.resolve();
    listeners[type](Object.assign({ waitUntil: (p) => { waited = p; } }, event));
    await waited;
  };
  return { run, shown, opened, focused, setWindows: (w) => { windows = w; } };
}

const push = (data) => ({ data: data === undefined ? null : { json: () => { if (data instanceof Error) throw data; return data; } } });

test('a message shows a notification with its title, body and tag', async () => {
  const w = worker();
  await w.run('push', push({ title: 'Ana mentioned you', body: 'in Plan', tag: 'mention-bo', url: '/d/plan' }));
  // (objects made inside the worker's context are compared as JSON)
  assert.equal(JSON.stringify(w.shown), JSON.stringify([{ title: 'Ana mentioned you', options: { body: 'in Plan', data: { url: '/d/plan' }, tag: 'mention-bo' } }]));
});

test('a message that is not what was expected still shows a notification (a push must always be visible)', async () => {
  const w = worker();
  await w.run('push', push(undefined));
  await w.run('push', push(new Error('not json')));
  await w.run('push', push('a string'));
  await w.run('push', push({ title: 7, body: { x: 1 }, url: 5 }));
  assert.equal(w.shown.length, 4);
  for (const s of w.shown) assert.equal(s.title, 'New activity');
  assert.equal(w.shown[3].options.body, '');
});

test('long fields are cut', async () => {
  const w = worker();
  await w.run('push', push({ title: 'T'.repeat(500), body: 'B'.repeat(500), tag: 'g'.repeat(500) }));
  assert.equal(w.shown[0].title.length, 120);
  assert.equal(w.shown[0].options.body.length, 240);
  assert.equal(w.shown[0].options.tag.length, 80);
});

function click(url) {
  let closed = false;
  return { notification: { data: { url }, close: () => { closed = true; } }, closed: () => closed };
}

test('a click opens the page of the app, or focuses the window that has it', async () => {
  const w = worker();
  const e = click('/editor/d/plan');
  await w.run('notificationclick', e);
  assert.deepEqual(w.opened, ['https://app.example/editor/d/plan']);
  assert.ok(e.closed());
  let focused = 0;
  w.setWindows([{ url: 'https://app.example/editor/d/plan', focus: () => { focused++; return Promise.resolve(); } }]);
  await w.run('notificationclick', click('/editor/d/plan'));
  assert.equal(focused, 1);
  assert.equal(w.opened.length, 1);
});

test('a click never leaves the origin of the app', async () => {
  const w = worker();
  await w.run('notificationclick', click('https://evil.example/phish'));
  await w.run('notificationclick', click('//evil.example/x'));
  await w.run('notificationclick', click('javascript:alert(1)'));
  await w.run('notificationclick', click(''));
  assert.deepEqual(w.opened, ['https://app.example/editor/', 'https://app.example/editor/', 'https://app.example/editor/', 'https://app.example/editor/']);
});
