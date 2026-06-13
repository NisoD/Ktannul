import { chromium } from 'playwright-core';

const BASE = 'https://mitayshvim.duckdns.org';
const OUT = process.env.OUT || '/tmp/mv-shots';
const log = (m) => console.log('▶ ' + m);

const browser = await chromium.launch({ channel: 'chrome' });
try {
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 800 }, deviceScaleFactor: 2 });
  const page = await ctx.newPage();

  // 1. landing
  await page.goto(BASE + '/', { waitUntil: 'load' });
  await page.waitForTimeout(600);
  await page.screenshot({ path: `${OUT}/landing.png` });
  log('landing.png');

  // 2. create room → join screen
  await page.click('#create');
  await page.waitForURL(/\/r\/[A-Z2-9]{6}$/, { timeout: 8000 });
  await page.waitForSelector('#nameInput', { timeout: 8000 });
  await page.waitForTimeout(800);
  await page.screenshot({ path: `${OUT}/join.png` });
  log('join.png');

  // 3. lobby with QR + room code
  await page.fill('#nameInput', 'Daniel');
  await page.click('text=Join game');
  await page.waitForSelector('#joinShare:not(.hidden)', { timeout: 8000 });
  await page.waitForTimeout(800);
  await page.screenshot({ path: `${OUT}/lobby.png` });
  log('lobby.png');

  // 4. solo vs bots → 3D board mid-setup
  await page.click('text=Solo vs bots');
  await page.waitForTimeout(4000); // bots place, 3D board renders
  await page.screenshot({ path: `${OUT}/board.png` });
  log('board.png');

  // 5. phone-sized lobby (portrait) in a second room
  const phone = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 3, isMobile: true, hasTouch: true });
  const p2 = await phone.newPage();
  const code = page.url().match(/[A-Z2-9]{6}$/)[0];
  await p2.goto(`${BASE}/r/${code}`, { waitUntil: 'load' });
  await p2.waitForTimeout(2500);
  await p2.screenshot({ path: `${OUT}/phone.png` });
  log('phone.png');

  console.log('SCREENSHOTS DONE');
} finally {
  await browser.close();
}
