import { chromium } from 'playwright-core';

const BASE = 'http://localhost:8091';
const log = (m) => console.log('▶ ' + m);

const browser = await chromium.launch({ channel: 'chrome' });
try {
  const ctxA = await browser.newContext();
  const ctxB = await browser.newContext();

  // create two rooms via API
  const mk = async () => (await (await fetch(BASE + '/api/rooms', { method: 'POST' })).json()).code;
  const roomA = await mk(), roomB = await mk();
  if (!roomA || !roomB) throw new Error('room creation failed');
  if (roomA === roomB) throw new Error('duplicate room codes');
  log(`rooms: ${roomA} ${roomB}`);

  const pageA = await ctxA.newPage();
  const errsA = [];
  pageA.on('pageerror', (e) => errsA.push(e.message));
  pageA.on('dialog', () => { throw new Error('XSS: dialog opened'); });
  await pageA.goto(`${BASE}/r/${roomA}`, { waitUntil: 'load' });
  await pageA.fill('#nameInput', 'Alice');
  await pageA.click('text=Join game');
  await pageA.waitForSelector('text=Alice', { timeout: 5000 });
  log('Alice joined room A');

  // hostile name joins room A via API — must render as inert text
  await fetch(`${BASE}/api/r/${roomA}/join`, {
    method: 'POST',
    body: JSON.stringify({ name: '<img src=x onerror=alert(1)>' }),
  });
  await pageA.waitForSelector('text=<img', { timeout: 5000 });
  if (await pageA.locator('#lobbyList img, #players img').count())
    throw new Error('XSS: hostile name created an element');
  log('hostile name rendered inert');

  const pageB = await ctxB.newPage();
  await pageB.goto(`${BASE}/r/${roomB}`, { waitUntil: 'load' });
  await pageB.fill('#nameInput', 'Bob');
  await pageB.click('text=Join game');
  await pageB.waitForSelector('text=Bob', { timeout: 5000 });
  log('Bob joined room B');

  // isolation both directions
  if (await pageB.locator('text=Alice').count()) throw new Error('LEAK: Alice visible in room B');
  if (await pageA.locator('text=Bob').count()) throw new Error('LEAK: Bob visible in room A');
  log('rooms isolated');

  // reconnect: reload keeps the seat (per-room localStorage)
  await pageA.reload({ waitUntil: 'load' });
  await pageA.waitForSelector('text=Alice', { timeout: 5000 });
  log('Alice kept seat after reload');

  // unknown room redirects to landing
  const pageC = await ctxA.newPage();
  await pageC.goto(`${BASE}/r/ZZZZZZ`, { waitUntil: 'load' });
  if (!pageC.url().includes('/?missing=1'))
    throw new Error('no redirect for unknown room: ' + pageC.url());
  log('unknown room redirected to landing');

  // landing create-button flow
  const pageD = await ctxB.newPage();
  await pageD.goto(`${BASE}/`, { waitUntil: 'load' });
  await pageD.click('#create');
  await pageD.waitForURL(/\/r\/[A-Z2-9]{6}$/, { timeout: 5000 });
  await pageD.waitForSelector('#nameInput', { timeout: 5000 });
  log('landing → create → room works');

  if (errsA.length) throw new Error('page errors: ' + errsA.join(' | '));
  console.log('ROOMS E2E PASS');
} finally {
  await browser.close();
}
