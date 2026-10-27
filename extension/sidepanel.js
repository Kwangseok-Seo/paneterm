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
let opened = 0; // labels count up and are never reused

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
  };
  tabs.push(tab);

  tab.button.className = 'tab';
  tab.button.innerHTML = '<span class="label"></span><button class="close" title="Close" aria-label="Close tab">×</button>';
  tab.button.querySelector('.label').textContent = String(++opened);
  tab.button.addEventListener('click', () => show(tab));
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

  connect(tab);
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
// Clicking the bar would otherwise take focus from the terminal.
bar.addEventListener('mousedown', (e) => e.preventDefault());
// Overlay scrollbars hide until scrolled, so a mouse wheel scrolls the tabs.
// Horizontal and pinch (ctrl) scrolling are left to the browser.
tabbar.addEventListener('wheel', (e) => {
  if (!e.ctrlKey && Math.abs(e.deltaY) > Math.abs(e.deltaX)) tabbar.scrollLeft += e.deltaY;
}, { passive: true });
openTab();
