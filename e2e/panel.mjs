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

// Chrome starts each host with the extension's origin as its argument.
const hosts = () => processes().filter((l) => l.includes('paneterm-host chrome-extension://'));
const shells = () => processes().filter((l) => !l.includes('paneterm-host'));

// Polls until list() has n entries, and returns the last list read.
async function settle(list, n, ms = 5000) {
  let got = list();
  for (const end = Date.now() + ms; got.length !== n && Date.now() < end; await sleep(50)) got = list();
  return got;
}

// Polls until ok() is true, for up to ms.
async function until(ok, ms = 3000) {
  for (const end = Date.now() + ms; Date.now() < end; await sleep(50)) {
    if (await ok()) return true;
  }
  return false;
}

// A new shell's screen holds its first prompt and nothing else.
const onlyPrompt = (s) => /^\S+#$/.test(s.trim());

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
      () => document.querySelector('.pane.active .xterm-screen').getBoundingClientRect().bottom - window.innerHeight,
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

  check('a new tab opens with a zsh prompt', await panel.newTab());
  let h = await settle(hosts, 2);
  check('each tab runs its own host', h.length === 2, `${h.length} hosts`);
  await panel.run('echo "tab2=$((5*5))"');
  await panel.waitFor('tab2=25');
  await panel.selectTab(0);
  await panel.screen();
  check('each tab keeps its own screen', panel.text.includes('seq-end=300k') && !panel.text.includes('tab2=25'));
  await panel.run('echo "back=$((7*3))"');
  check('typing goes to the shown tab', await panel.waitFor('back=21'));

  // Tab 1's shell writes its size on SIGWINCH to a file that tab 2 reads, so
  // the size is the one it got while hidden, not on being shown again.
  await panel.run(`trap 'echo "hidden=$(stty size)" >/tmp/winch' WINCH`);
  const shown = await panel.size();
  await panel.selectTab(1);
  await panel.page.setViewport({ width: 600, height: 400 });
  const seen = await panel.size();
  await panel.run('for i in {1..20}; do [[ -s /tmp/winch ]] && break; sleep 0.1; done; cat /tmp/winch');
  const hidden = await panel.waitFor(`hidden=${seen}`, 3000);
  check('a tab in the background follows the panel size', hidden && seen !== shown, `${shown} -> ${seen}`);
  await panel.selectTab(0);
  const back = await panel.size();
  check('a tab shown again shows its last line', back === seen, `size line read: ${back}`);
  await panel.page.setViewport({ width: 800, height: 500 });

  await panel.closeTab(1);
  h = await settle(hosts, 1);
  let sh = await settle(shells, 1);
  check('closing a tab stops its host and shell', h.length === 1 && sh.length === 1,
    `${h.length} hosts, ${sh.length} shells`);
  await panel.run('echo "still=$((8+8))"');
  check('the other tab keeps running', await panel.waitFor('still=16'));

  // Tabs 1 X Y Z: closing X while shown must show Y, the tab after it, and
  // closing Z while shown, now last, must show Y, the one before. The tab
  // shown must also get the keys.
  await panel.newTab();
  await panel.newTab();
  await panel.run('echo "y=$((4*6))"');
  await panel.waitFor('y=24');
  await panel.newTab();
  await panel.selectTab(1);
  await panel.closeTab(1);
  await panel.run('echo "next=$((9+1))"');
  check('closing the shown tab shows the one after it', await panel.waitFor('next=10') && panel.text.includes('y=24'));
  await panel.selectTab(2);
  await panel.closeTab(2);
  await panel.run('echo "prev=$((6+5))"');
  check('closing the shown last tab shows the one before it',
    await panel.waitFor('prev=11') && panel.text.includes('next=10'));
  await panel.closeTab(1);

  // A tab's label is a name given by hand, else the title the shell or a
  // program sets, else the tab's number. The shell computes each title, so
  // none can come from the echoed command line.
  await panel.run(`printf '\\e]0;t%s\\a' $((6*7))`);
  check('a title set by a program names the tab', await panel.waitLabel(0, 't42'), await panel.label(0));
  // The title a Windows console gave a new pwsh, as measured.
  await panel.run(`printf '\\e]0;%s\\a' 'C:\\Program Files\\WindowsApps\\Microsoft.PowerShell_7.6.9.0_x64__8wekyb3d8bbwe\\pwsh.exe'`);
  check('a Windows shell path as title leaves the number', await panel.waitLabel(0, '1'), await panel.label(0));
  await panel.run(`printf '\\e]0;t%s\\a' $((7*7))`);
  await panel.waitLabel(0, 't49');
  // A command typed but not yet run, which the Enter ending the rename
  // must not run.
  await panel.page.keyboard.type('echo "leak=$((6*7))"');
  await panel.startRename(0);
  await panel.page.keyboard.type('ork');
  // Clicking the field's left edge puts the caret before the text.
  await (await panel.page.$('.rename'))?.click({ offset: { x: 2, y: 5 } });
  await panel.page.keyboard.type('w');
  await panel.page.keyboard.press('Enter');
  check('double-clicking a tab renames it, and clicks in the name move its caret',
    await panel.waitLabel(0, 'work'), await panel.label(0));
  // Ctrl-U drops the typed command.
  await panel.page.keyboard.down('Control');
  await panel.page.keyboard.press('u');
  await panel.page.keyboard.up('Control');
  await panel.run(`printf '\\e]0;t%s\\a' $((8*8)); echo "titled=$((5*9))"`);
  await panel.waitFor('titled=45');
  check('a name given by hand outlasts later titles', (await panel.label(0)) === 'work', await panel.label(0));
  check('the Enter ending a rename stays out of the shell', !panel.text.includes('leak=42'));

  await panel.startRename(0);
  await panel.page.keyboard.type('tabbed');
  await panel.page.keyboard.press('Tab');
  await panel.run('echo "tab=$((3*5))"');
  check('Tab keeps the name and gives the keys back to the shell',
    await panel.waitFor('tab=15') && (await panel.label(0)) === 'tabbed', await panel.label(0));

  // Focus going to another window and back leaves the field open.
  await panel.startRename(0);
  await panel.page.keyboard.type('half');
  const focused = () => panel.page.evaluate(() => document.hasFocus());
  const away = await browser.newPage();
  await away.bringToFront();
  const left = await until(async () => !(await focused()));
  await panel.page.bringToFront();
  await away.close();
  await until(focused);
  await panel.page.keyboard.type('way');
  await panel.page.keyboard.press('Enter');
  check('leaving the window keeps the name being typed', left && await panel.waitLabel(0, 'halfway'),
    `${await panel.label(0)}, window left: ${left}`);

  // An IME takes the first Enter to end its composition; only the next one
  // ends the rename.
  await panel.startRename(0);
  await panel.page.keyboard.type('작');
  const ime = await panel.page.createCDPSession();
  await ime.send('Input.imeSetComposition', { text: '업', selectionStart: 1, selectionEnd: 1 });
  await panel.page.keyboard.press('Enter');
  const composing = (await panel.page.$$('.rename')).length === 1;
  await ime.send('Input.insertText', { text: '업' });
  await panel.page.keyboard.press('Enter');
  check('the Enter ending an IME composition stays in the name',
    composing && await panel.waitLabel(0, '작업'), `${await panel.label(0)}, field open after it: ${composing}`);

  await panel.startRename(0);
  await panel.page.keyboard.type('zzz');
  await panel.page.keyboard.press('Escape');
  await panel.run('echo "esc=$((2*9))"');
  check('Escape keeps the old name and gives the keys back to the shell',
    await panel.waitFor('esc=18') && (await panel.label(0)) === '작업', await panel.label(0));
  await panel.startRename(0);
  await panel.page.keyboard.press('Enter');
  check('an empty name hands the label back to the title', await panel.waitLabel(0, 't64'), await panel.label(0));
  await panel.run(`printf '\\e]0;\\a'`);
  check('an empty title gives the tab its number back', await panel.waitLabel(0, '1'), await panel.label(0));

  // In a narrow panel, tab 1's long name overflows the bar once a second
  // tab opens, which has to come into view whole, label and ×.
  await panel.page.setViewport({ width: 260, height: 500 });
  await panel.startRename(0);
  await panel.page.keyboard.type('a name long enough to overflow');
  await panel.page.keyboard.press('Enter');
  await panel.newTab();
  const past = await panel.page.evaluate(() =>
    [...document.querySelectorAll('.tab .close')].pop().getBoundingClientRect().right
      - document.getElementById('tabs').getBoundingClientRect().right);
  check('a new tab scrolls into view whole', past <= 0, `its × ends ${past}px past the bar`);
  await panel.page.setViewport({ width: 800, height: 500 });
  await panel.selectTab(0);
  await panel.startRename(0);
  await panel.page.keyboard.press('Enter');
  await panel.selectTab(1);

  // Double-clicking × closes tab 1, and the second click lands on the label
  // of the wider tab that slides under the pointer.
  await panel.run(`printf '\\e]0;wide-%s-title\\a' $((3*3))`);
  await panel.waitLabel(1, 'wide-9-title');
  const [x, y] = await panel.page.$$eval('.tab .close', ([close]) => {
    const r = close.getBoundingClientRect();
    return [r.x + r.width / 2, r.y + r.height / 2];
  });
  await panel.page.mouse.click(x, y, { count: 2 });
  const under = await panel.page.$$eval('.tab .label', ([label], x) => {
    const r = label.getBoundingClientRect();
    return r.left <= x && x <= r.right;
  }, x);
  const fields = (await panel.page.$$('.rename')).length;
  check("a double click closing a tab doesn't rename the next one", under && fields === 0,
    `second click on its label: ${under}, ${fields} fields`);

  const second = await openPanel();
  h = await settle(hosts, 2);
  check('two panels run two hosts', h.length === 2, `${h.length} hosts`);
  await second.run('echo "old=$((4*4))"');
  await second.waitFor('old=16');
  await second.closeTab(0);
  const fresh = await second.waitFor(onlyPrompt);
  h = await settle(hosts, 2);
  check('closing the only tab starts a new shell', fresh && h.length === 2, `${h.length} hosts`);
  await second.newTab();
  const full = await settle(hosts, 3);
  await second.page.close();
  h = await settle(hosts, 1);
  sh = await settle(shells, 1);
  check('closing a panel stops the hosts and shells of all its tabs',
    full.length === 3 && h.length === 1 && sh.length === 1,
    `${full.length} -> ${h.length} hosts, ${sh.length} shells left`);

  await panel.run('exit 3');
  check('exit shows the code', await panel.waitFor('[process exited with code 3]'));
  await panel.page.keyboard.press('Enter');
  await panel.run('echo "again=$((1+1))"');
  check('Enter starts a new shell', await panel.waitFor('again=2'));
  // The shell that titled this tab "wide-9-title" is gone.
  check('a new shell starts untitled', /^\d+$/.test(await panel.label(0)), await panel.label(0));

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
    sizes: 0,
    async screen() {
      this.text = await page.evaluate(() =>
        [...document.querySelectorAll('.pane.active .xterm-rows > div')]
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
    // Reading only a fresh prompt, this can't pass on the tab shown before.
    async newTab() {
      await page.click('#new-tab');
      return this.waitFor(onlyPrompt);
    },
    // Each call reads a size line of its own, never one left on screen.
    async size() {
      const tag = `size${++this.sizes}`;
      await this.run(`echo "${tag}=$(stty size)"`);
      const re = new RegExp(`^${tag}=(\\d+ \\d+)$`, 'm');
      return (await this.waitFor(re, 3000)) ? this.text.match(re)[1] : undefined;
    },
    async selectTab(i) {
      await (await page.$$('.tab .label'))[i].click();
    },
    async closeTab(i) {
      await (await page.$$('.tab .close'))[i].click();
    },
    label(i) {
      return page.$$eval('.tab .label', (labels, i) => labels[i]?.textContent, i);
    },
    waitLabel(i, want) {
      return until(async () => (await this.label(i)) === want);
    },
    // Double-clicks the tab's label and clears the field that opens.
    async startRename(i) {
      await (await page.$$('.tab .label'))[i].click({ count: 2 });
      await page.keyboard.press('Backspace');
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
