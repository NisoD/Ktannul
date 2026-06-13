import { chromium } from 'playwright-core';

const BASE = 'http://localhost:8092';
const log = (m) => console.log('▶ ' + m);

const browser = await chromium.launch({ channel: 'chrome' });
try {
  // create a room + start a solo game so we have a live board
  const code = (await (await fetch(BASE + '/api/rooms', { method: 'POST' })).json()).code;
  log('room ' + code);

  const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));
  await page.goto(`${BASE}/r/${code}`, { waitUntil: 'load' });
  await page.fill('#nameInput', 'Host');
  await page.click('text=Join game');
  await page.click('text=Solo vs bots');
  await page.waitForTimeout(2500);

  // --- emoji: send one, assert a float element appears ---
  await page.waitForSelector('#emojiToggle.show', { timeout: 5000 });
  await page.click('#emojiToggle');
  await page.waitForSelector('#emojiBar.open', { timeout: 3000 });
  await page.click('#emojiBar button:has-text("🎉")');
  await page.waitForSelector('.emoji-float', { timeout: 3000 });
  log('emoji float appeared');

  // --- metrics endpoint + page ---
  const m = await (await fetch(BASE + '/api/metrics')).json();
  if (typeof m.gamesCreated !== 'number' || typeof m.playersJoined !== 'number' || typeof m.gamesLive !== 'number')
    throw new Error('metrics shape wrong: ' + JSON.stringify(m));
  log(`metrics: created=${m.gamesCreated} players=${m.playersJoined} live=${m.gamesLive}`);
  const sp = await ctx.newPage();
  await sp.goto(`${BASE}/stats`, { waitUntil: 'load' });
  await sp.waitForFunction(() => document.getElementById('created').textContent !== '—', { timeout: 5000 });
  log('stats page renders numbers');

  // --- spectator: read-only watcher sees the board, no action buttons ---
  const watch = await browser.newContext({ viewport: { width: 1100, height: 800 } });
  const wp = await watch.newPage();
  await wp.goto(`${BASE}/r/${code}?watch=1`, { waitUntil: 'load' });
  await wp.waitForTimeout(1500);
  await wp.waitForSelector('text=Spectating', { timeout: 5000 });
  const actionsHTML = await wp.locator('#actions').innerHTML();
  if (actionsHTML.trim() !== '') throw new Error('spectator has action controls: ' + actionsHTML.slice(0, 80));
  // spectator must NOT see a join name field as the primary screen
  if (await wp.locator('#joinScreen:not(.hidden)').count()) throw new Error('spectator stuck on join screen');
  log('spectator read-only board OK');

  // --- XSS: hostile name renders inert for the spectator too ---
  wp.on('dialog', () => { throw new Error('XSS dialog'); });
  await fetch(`${BASE}/api/r/${code}/join`, { method: 'POST', body: JSON.stringify({ name: '<img src=x onerror=alert(1)>' }) });
  await wp.waitForTimeout(800);
  if (await wp.locator('#players img').count()) throw new Error('XSS: hostile name created an element');
  log('hostile name inert');

  if (errs.length) throw new Error('page errors: ' + errs.join(' | '));
  console.log('FEATURES E2E PASS');
} finally {
  await browser.close();
}
