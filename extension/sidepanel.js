import { Terminal } from './vendor/xterm/xterm.mjs';
import { FitAddon } from './vendor/xterm/addon-fit.mjs';

const HOST = 'io.github.kwangseok_seo.paneterm';

const bar = document.getElementById('bar');
const tabbar = document.getElementById('tabs');
const panes = document.getElementById('panes');

// Each tab has its own terminal and port, and one port is one host process
// is one shell. A tab's port is null while no shell is running.
const tabs = [];
let current = null;
let opened = 0; // tab numbers count up and are never reused
// The tab under the first click of a double click. One closing slides the
// next under the pointer, so the second click can land on another tab.
let pressed = null;

// Panes all fill #panes, so its size is every terminal's size. Hidden panes
// keep their layout, so tabs in the background follow the panel's size too,
// at the cost of still rendering their output. (A terminal under display:
// none pauses, and one resized as it came back was left scrolled up from its
// last line.)
new ResizeObserver(() => {
  for (const tab of tabs) tab.fit.fit();
}).observe(panes);

function openTab() {
  const tab = {
    term: new Terminal({
      cursorBlink: true,
      fontFamily: 'Menlo, Monaco, "Courier New", monospace',
      fontSize: 13,
    }),
    fit: new FitAddon(),
    port: null,
    pane: document.createElement('div'),
    button: document.createElement('div'),
    number: ++opened,
    name: null, // given by hand
    title: '', // set by the shell or a program in it
  };
  tabs.push(tab);

  tab.button.className = 'tab';
  tab.button.innerHTML = '<span class="label"></span><button class="close" title="Close" aria-label="Close tab">×</button>';
  tab.label = tab.button.querySelector('.label');
  // Before show(), which scrolls the tab into view at its labeled width.
  relabel(tab);
  tab.button.addEventListener('click', (e) => {
    show(tab);
    if (e.detail === 2 && pressed === tab.button) rename(tab);
  });
  tab.button.querySelector('.close').addEventListener('click', (e) => {
    e.stopPropagation();
    // The next tab slides under the pointer, so the second click of a
    // double click would close its shell too.
    if (e.detail < 2) closeTab(tab);
  });
  tabbar.append(tab.button);

  tab.pane.className = 'pane';
  panes.append(tab.pane);
  tab.term.loadAddon(tab.fit);
  tab.term.open(tab.pane);
  tab.fit.fit();
  show(tab);

  tab.term.onData((data) => {
    if (!tab.port) {
      if (data === '\r') connect(tab);
      return;
    }
    tab.port.postMessage({ t: 'in', d: toBase64(utf8.encode(data)) });
  });

  // Some mouse reports arrive as binary strings, one char per byte, which is
  // what btoa takes.
  tab.term.onBinary((data) => tab.port?.postMessage({ t: 'in', d: btoa(data) }));

  tab.term.onResize(({ cols, rows }) => tab.port?.postMessage({ t: 'resize', cols, rows }));

  tab.term.onTitleChange((title) => {
    // A Windows console titles itself with the path of the shell it started,
    // which would give every tab the same name.
    tab.title = /^[a-z]:\\.*\.exe$/i.test(title) ? '' : title.trim();
    relabel(tab);
  });

  connect(tab);
}

// A name given by hand wins over the title, and the tab's number stands in
// for both.
function relabel(tab) {
  const text = tab.name || tab.title || String(tab.number);
  // Some programs retitle at every prompt or step of progress.
  if (tab.label.textContent === text) return;
  tab.label.textContent = text;
  // Long labels are cut short; this shows them whole.
  tab.button.title = text;
}

// Puts a field over the label. Enter, Tab or clicking away keeps what was
// typed, Escape drops it, and an empty name hands the label back to the
// title.
function rename(tab) {
  const { label } = tab;
  const field = document.createElement('input');
  field.className = 'rename';
  field.value = label.textContent;
  const before = field.value;
  let done = false;
  const finish = (keep) => {
    if (done) return;
    done = true;
    if (keep && field.value !== before) tab.name = field.value.trim() || null;
    field.remove();
    label.hidden = false;
    relabel(tab);
  };
  field.addEventListener('keydown', (e) => {
    // The Enter that ends an IME composition belongs to the composition.
    if (e.isComposing || !['Enter', 'Tab', 'Escape'].includes(e.key)) return;
    // Otherwise the key acts again where focus lands: the terminal takes
    // Enter as typed, and Tab moves focus on to the ×.
    e.preventDefault();
    finish(e.key !== 'Escape');
    tab.term.focus();
  });
  // Leaving the window isn't leaving the field, which has focus again on
  // coming back.
  field.addEventListener('blur', () => document.hasFocus() && finish(true));
  // Clicks in the field move its caret; on the tab they would focus the
  // terminal.
  field.addEventListener('click', (e) => e.stopPropagation());
  label.hidden = true;
  label.after(field);
  field.focus();
  field.select();
}

function show(tab) {
  current = tab;
  for (const t of tabs) {
    t.pane.classList.toggle('active', t === tab);
    t.button.classList.toggle('active', t === tab);
  }
  tab.button.scrollIntoView({ block: 'nearest', inline: 'nearest' });
  tab.term.focus();
}

function closeTab(tab) {
  // The host exits when its port closes, and takes the shell with it.
  tab.port?.disconnect();
  tab.term.dispose();
  tab.pane.remove();
  tab.button.remove();
  const i = tabs.indexOf(tab);
  tabs.splice(i, 1);
  if (!tabs.length) {
    openTab();
  } else if (tab === current) {
    show(tabs[Math.min(i, tabs.length - 1)]);
  }
}

function connect(tab) {
  const { term } = tab;
  // A new shell starts untitled, whatever the last one left.
  tab.title = '';
  relabel(tab);
  const port = chrome.runtime.connectNative(HOST);
  tab.port = port;
  port.onMessage.addListener((m) => onMessage(tab, m));
  port.onDisconnect.addListener(() => {
    tab.port = null;
    // Set when the host is missing, not allowed, or died without saying why.
    const reason = chrome.runtime.lastError?.message ?? 'disconnected';
    term.write(`\r\n\x1b[31m[paneterm: ${reason}]\x1b[0m`);
    if (reason.includes('not found')) {
      term.write('\r\nInstall the host with: paneterm-host install');
    }
    offerRestart(tab);
  });
  port.postMessage({ t: 'open', cols: term.cols, rows: term.rows });
}

function onMessage(tab, m) {
  switch (m.t) {
    case 'out':
      tab.term.write(fromBase64(m.d));
      break;
    case 'exit':
      // Disconnecting ourselves keeps onDisconnect from reporting the exit again.
      tab.port.disconnect();
      tab.port = null;
      tab.term.write(`\r\n[process exited with code ${m.code}]`);
      offerRestart(tab);
      break;
    case 'error':
      tab.term.write(`\r\n\x1b[31m[paneterm-host: ${m.msg}]\x1b[0m`);
      break;
  }
}

function offerRestart(tab) {
  tab.term.write('\r\nPress Enter to start a new shell.\r\n');
}

const utf8 = new TextEncoder();

function toBase64(bytes) {
  let s = '';
  for (const b of bytes) s += String.fromCharCode(b);
  return btoa(s);
}

function fromBase64(text) {
  const s = atob(text);
  const bytes = new Uint8Array(s.length);
  for (let i = 0; i < s.length; i++) bytes[i] = s.charCodeAt(i);
  return bytes;
}

// When the last tab closes, + slides under the pointer instead, and the
// second click of a double click on its × would start a shell.
document.getElementById('new-tab').addEventListener('click', (e) => e.detail < 2 && openTab());
// Clicking the bar would otherwise take focus from the terminal. A field
// renaming a tab takes clicks of its own.
bar.addEventListener('mousedown', (e) => e.target.matches('.rename') || e.preventDefault());
bar.addEventListener('click', (e) => {
  if (e.detail === 1) pressed = e.target.closest('.tab');
}, true);
// Overlay scrollbars hide until scrolled, so a mouse wheel scrolls the tabs.
// Horizontal and pinch (ctrl) scrolling are left to the browser.
tabbar.addEventListener('wheel', (e) => {
  if (!e.ctrlKey && Math.abs(e.deltaY) > Math.abs(e.deltaX)) tabbar.scrollLeft += e.deltaY;
}, { passive: true });
openTab();
