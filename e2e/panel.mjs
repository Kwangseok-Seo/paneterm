// Drives the real extension in Chromium: the panel page talks to
// paneterm-host over native messaging, and the host runs zsh on a PTY.
import { execFileSync } from 'node:child_process';
import puppeteer from 'puppeteer-core';

const EXT = '/work/extension';
const HOST = '/opt/paneterm/paneterm-host';
const t0 = Date.now();
const failures = [];

function log(msg) {
  console.log(`${String(Date.now() - t0).padStart(6)}ms  ${msg}`);
}

function check(what, ok, detail = '') {
  log(`${ok ? 'PASS' : 'FAIL'}  ${what}${detail ? `  (${detail})` : ''}`);
  if (!ok) failures.push(what);
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function processes() {
  return execFileSync('ps', ['-eo', 'pid,args'], { encoding: 'utf8' })
    .split('\n')
    .filter((l) => /paneterm-host|zsh/.test(l));
}

const installed = execFileSync(HOST, ['install'], { encoding: 'utf8' });
const id = installed.match(/allowed extension: (\w+)/)[1];
log(`paneterm-host install -> allowed ${id}`);
let extensionId; // the ID Chromium actually gave the loaded extension

const browser = await puppeteer.launch({
  executablePath: '/usr/bin/chromium',
  headless: true,
  // Chrome reads a user's host manifests from its profile directory,
  // which is where `paneterm-host install` wrote one.
  userDataDir: `${process.env.HOME}/.config/google-chrome`,
  ignoreDefaultArgs: ['--disable-extensions'],
  args: ['--no-sandbox', '--disable-dev-shm-usage', `--disable-extensions-except=${EXT}`, `--load-extension=${EXT}`],
});

try {
  const worker = await browser.waitForTarget(
    (t) => t.type() === 'service_worker' && t.url().startsWith('chrome-extension://'),
    { timeout: 10_000 },
  );
  extensionId = new URL(worker.url()).host;
  check('Chromium gives the extension the ID the installer allowed', extensionId === id, extensionId);

  const panel = await openPanel();

  await panel.run('echo "sum=$((6*7))"');
  check('shell runs a command', await panel.waitFor('sum=42'));

  // Chromium passes no LANG here, so this is the host's Linux fallback,
  // glibc's built-in C.UTF-8; the image generates no other locale.
  await panel.run('echo "lang=$LANG"');
  check('the shell gets a UTF-8 locale', await panel.waitFor('lang=C.UTF-8'), panel.lastLine('lang='));

  await panel.run("printf 'got:%s\\n' 한글");
  check('non-ASCII input and output', await panel.waitFor('got:한글'), panel.lastLine('got:'));

  await panel.run('stty size');
  await panel.waitFor(/^\d+ \d+$/m);
  const before = panel.lastMatch(/^(\d+ \d+)$/gm);
  await panel.page.setViewport({ width: 480, height: 300 });
  await sleep(300);
  await panel.run('stty size');
  await panel.waitFor(() => panel.lastMatch(/^(\d+ \d+)$/gm) !== before, 3000);
  const after = panel.lastMatch(/^(\d+ \d+)$/gm);
  check('resizing the panel resizes the PTY', before && after && before !== after, `${before} -> ${after}`);
  // FitAddon must leave every row inside the panel at any height.
  const clipped = [];
  for (let height = 300; height <= 340; height += 2) {
    await panel.page.setViewport({ width: 800, height });
    await sleep(100);
    const overflow = await panel.page.evaluate(
      () => document.querySelector('.xterm-screen').getBoundingClientRect().bottom - window.innerHeight,
    );
    if (overflow > 0) clipped.push(`${height}:${overflow}px`);
  }
  check('no height clips the last row', clipped.length === 0, clipped.join(' ') || '21 heights');
  await panel.page.setViewport({ width: 800, height: 500 });

  await panel.run('sleep 30');
  await sleep(300);
  await panel.page.keyboard.down('Control');
  await panel.page.keyboard.press('c');
  await panel.page.keyboard.up('Control');
  await panel.run('echo "after=$((2+2))"');
  check('Ctrl-C interrupts the foreground command', await panel.waitFor('after=4', 3000));

  const start = Date.now();
  // The shell computes the marker, so the echoed command line can't match it.
  await panel.run('seq 1 300000; echo "seq-end=$((300000/1000))k"');
  check('300k lines of output arrive', await panel.waitFor('seq-end=300k', 30_000), `${Date.now() - start}ms`);

  const second = await openPanel();
  const hosts = processes().filter((l) => l.includes('paneterm-host chrome-extension://'));
  check('two panels run two hosts', hosts.length === 2, `${hosts.length} hosts`);
  await second.page.close();
  await sleep(1000);
  const left = processes().filter((l) => l.includes('paneterm-host chrome-extension://'));
  check('closing a panel stops its host', left.length === 1, `${left.length} hosts left`);

  await panel.run('exit 3');
  check('exit shows the code', await panel.waitFor('[process exited with code 3]'));
  await panel.page.keyboard.press('Enter');
  await panel.run('echo "again=$((1+1))"');
  check('Enter starts a new shell', await panel.waitFor('again=2'));

  execFileSync(HOST, ['uninstall']);
  await panel.run('exit');
  check('exit 0 is reported', await panel.waitFor('[process exited with code 0]'));
  await panel.page.keyboard.press('Enter');
  check('a missing host is explained', await panel.waitFor('paneterm-host install'), panel.lastLine('paneterm:'));
  execFileSync(HOST, ['install']);

  await panel.page.keyboard.press('Enter');
  await panel.run('echo "live=$((3+3))"');
  // Without a live session here, the cleanup check below would pass vacuously.
  check('reinstalling lets the panel reconnect', await panel.waitFor('live=6'));
  await panel.page.close();
  await sleep(1000);
  const leftovers = processes();
  check('closing the last panel leaves no host or shell', leftovers.length === 0, leftovers.join(' | '));

  // A web page must have no channel to the extension.
  const web = await browser.newPage();
  await web.setRequestInterception(true);
  web.on('request', (r) => r.respond({ contentType: 'text/html', body: '<p>web</p>' }));
  await web.goto('https://example.test/');
  const exposed = await web.evaluate(() => typeof globalThis.chrome?.runtime?.connect);
  check('web pages cannot reach the extension', exposed === 'undefined', `chrome.runtime.connect is ${exposed}`);
} finally {
  await browser.close();
}

if (failures.length) {
  console.log(`\n${failures.length} failed`);
  process.exit(1);
}
console.log('\nall passed');

async function openPanel() {
  const page = await browser.newPage();
  await page.setViewport({ width: 800, height: 500 });
  await page.goto(`chrome-extension://${extensionId}/sidepanel.html`);
  const panel = {
    page,
    text: '',
    async screen() {
      this.text = await page.evaluate(() =>
        [...document.querySelectorAll('.xterm-rows > div')]
          .map((row) => row.textContent.replace(/ /g, ' ').trimEnd())
          .join('\n'),
      );
      return this.text;
    },
    async waitFor(want, ms = 5000) {
      for (const end = Date.now() + ms; Date.now() < end; await sleep(50)) {
        const s = await this.screen();
        if (typeof want === 'string' ? s.includes(want) : typeof want === 'function' ? want(s) : want.test(s)) return true;
      }
      log(`  screen at timeout:\n${this.text}`);
      return false;
    },
    async run(line) {
      await page.keyboard.type(line);
      await page.keyboard.press('Enter');
    },
    lastLine(prefix) {
      return this.text.split('\n').filter((l) => l.includes(prefix)).pop() ?? '';
    },
    lastMatch(re) {
      return [...this.text.matchAll(re)].pop()?.[1];
    },
  };
  // zsh's root prompt is "host# "; rows are read with trailing spaces trimmed.
  const ok = await panel.waitFor(/#$/m);
  check('panel opens with a zsh prompt', ok);
  return panel;
}
