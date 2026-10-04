// Drives canvas-editor-showcase as a shared document: three browsers (an
// owner, an editor, a viewer) on one document through a collab hub.
const { assert, open, run } = require(process.env.E2E_LIB);

const q = (name, token, color) =>
  `?doc=live1&hub=${encodeURIComponent(process.env.HUB_URL)}&token=${token}&transport=${process.env.TRANSPORT || 'http'}&flow=${encodeURIComponent(process.env.FLOW || '')}&name=${name}&color=${encodeURIComponent(color)}`;
const live = async (d) => (await d.state()).live;
const until = (d, what, fn, ms = 10000) =>
  d.page.waitForFunction(fn, null, { timeout: ms }).catch(async (e) => { throw new Error('timed out waiting for ' + what + ': ' + JSON.stringify((await d.state()).live)); });
// The page-side helper the waits use.
async function launch(query) {
  const d = await open(query);
  await d.page.evaluate(() => { window.stateText = () => JSON.parse(document.getElementById('state').textContent); });
  return d;
}

async function edit(d, atX, atY, key) {
  await d.click(atX, atY);
  await d.page.focus('#input');
  if (key) await d.page.keyboard.press(key);
}

run(async () => {
  const a = await launch(q('Ana', 'owner', '#e11d48'));
  await until(a, 'Ana online', () => stateText().live.status === 'online' && stateText().live.role === 'owner');
  assert((await a.state()).role === 'owner', 'the owner is an owner');
  await a.page.waitForTimeout(400); // her content reaches the hub

  const b = await launch(q('Bo', 'editor', '#2563eb'));
  await until(b, 'Bo online', () => stateText().live.status === 'online' && stateText().live.role === 'editor');
  const aText = (await a.state()).text;
  await until(b, 'Bo to get the document', () => stateText().text.length > 100);
  assert((await b.state()).text === aText, 'the second browser receives the first one\'s document');

  // Both go into edit mode; Ana types at the end of a line, Bo on another line.
  await a.toggleMode();
  await b.toggleMode();
  await edit(a, 240, 164, 'End');
  await a.page.keyboard.type(' ANA');
  await until(b, 'Ana\'s typing to reach Bo', () => stateText().text.includes(' ANA'));
  await edit(b, 240, 164 + 22, 'End');
  await b.page.keyboard.type(' BO');
  await until(a, 'Bo\'s typing to reach Ana', () => stateText().text.includes(' BO'));
  await until(b, 'Ana\'s text to be there too', () => stateText().text.includes(' ANA'));
  assert((await a.state()).text === (await b.state()).text, 'concurrent typing converges: both documents are the same');

  // Presence: each sees the other and a cursor drawn for them.
  await until(a, 'Ana to see Bo', () => stateText().live.peers.includes('Bo') && stateText().live.remote >= 1);
  await until(b, 'Bo to see Ana', () => stateText().live.peers.includes('Ana') && stateText().live.remote >= 1);
  assert(await a.page.locator('#presence .gj-presence-avatar').count() === 2, 'the presence list shows two people');
  assert((await a.page.locator('#presence .gj-presence-status').textContent()).startsWith('Online'), 'and that it is online');

  // Per-user undo: Ana undoes her typing; Bo's stays.
  await a.page.focus('#input');
  await a.page.keyboard.press('Control+z');
  await until(b, 'the undo to reach Bo', () => !stateText().text.includes(' ANA'));
  await until(a, 'the undo in Ana', () => !stateText().text.includes(' ANA') && stateText().text.includes(' BO'));
  assert((await b.state()).text.includes(' BO'), 'Ctrl+Z undid only Ana\'s own typing, Bo\'s stayed');
  await a.page.keyboard.press('Control+Shift+z');
  await until(b, 'the redo to reach Bo', () => stateText().text.includes(' ANA'));
  assert(true, 'redo brings it back for everyone');

  // A viewer: held in view mode, cannot type, can comment on selected text.
  const c = await launch(q('Vee', 'viewer', '#059669'));
  await until(c, 'Vee online as a viewer', () => stateText().live.status === 'online' && stateText().live.role === 'viewer');
  await until(c, 'Vee to get the document', () => stateText().text.length > 100);
  assert(await c.page.locator('#presence .gj-presence-badge').textContent() === 'View only', 'a viewer sees the View only badge');
  const before = (await c.state()).text;
  await c.toggleMode();
  assert((await c.state()).mode === 'view', 'a viewer cannot switch to edit mode');
  await c.click(240, 164);
  await c.page.keyboard.type('nope');
  assert((await c.state()).text === before, 'typing does nothing for a viewer');
  await c.page.mouse.dblclick(...c.at(240, 164)); // selects a word
  await c.page.click('button[data-tab="comments"]');
  c.page.once('dialog', (dlg) => dlg.accept('A viewer\'s comment'));
  await c.page.click('[data-action="add-comment"]');
  await until(a, 'the viewer\'s comment to reach Ana', () => stateText().comments === 1);
  assert((await c.state()).live.error === '', 'the server accepted the viewer\'s comment');
  assert((await a.state()).text === (await c.state()).text, 'and commenting changed no text');
  await a.page.click('button[data-tab="comments"]');
  assert(await a.page.locator('.gj-comment .gj-markdown').first().textContent() === 'A viewer\'s comment', 'Ana sees it in her comments list');
  assert(await c.page.locator('.gj-comment-reply-row').count() === 1 && await c.page.locator('.gj-comment-actions').count() === 0, 'the viewer can reply but not resolve or delete');

  // Closing a tab removes its cursor for the others.
  await c.close();
  await until(a, 'Vee to leave', () => !stateText().live.peers.includes('Vee'), 40000);
  assert(true, 'a closed browser leaves the presence list');
  assert(a.errors.length === 0 && b.errors.length === 0, 'no page errors: ' + a.errors.concat(b.errors).join('; '));
  await a.close();
  await b.close();
});
