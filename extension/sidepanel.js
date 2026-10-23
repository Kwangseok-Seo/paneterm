import { Terminal } from './vendor/xterm/xterm.mjs';
import { FitAddon } from './vendor/xterm/addon-fit.mjs';

const HOST = 'io.github.kwangseok_seo.paneterm';

const container = document.getElementById('terminal');
const term = new Terminal({
  cursorBlink: true,
  fontFamily: 'Menlo, Monaco, "Courier New", monospace',
  fontSize: 13,
});
const fit = new FitAddon();
term.loadAddon(fit);
term.open(container);
fit.fit();
new ResizeObserver(() => fit.fit()).observe(container);

// One port is one host process is one shell. null means no shell is running.
let port = null;

function connect() {
  port = chrome.runtime.connectNative(HOST);
  port.onMessage.addListener(onMessage);
  port.onDisconnect.addListener(() => {
    port = null;
    // Set when the host is missing, not allowed, or died without saying why.
    const reason = chrome.runtime.lastError?.message ?? 'disconnected';
    term.write(`\r\n\x1b[31m[paneterm: ${reason}]\x1b[0m`);
    if (reason.includes('not found')) {
      term.write('\r\nInstall the host with: paneterm-host install');
    }
    offerRestart();
  });
  port.postMessage({ t: 'open', cols: term.cols, rows: term.rows });
  term.focus();
}

function onMessage(m) {
  switch (m.t) {
    case 'out':
      term.write(fromBase64(m.d));
      break;
    case 'exit':
      // Disconnecting ourselves keeps onDisconnect from reporting the exit again.
      port.disconnect();
      port = null;
      term.write(`\r\n[process exited with code ${m.code}]`);
      offerRestart();
      break;
    case 'error':
      term.write(`\r\n\x1b[31m[paneterm-host: ${m.msg}]\x1b[0m`);
      break;
  }
}

function offerRestart() {
  term.write('\r\nPress Enter to start a new shell.\r\n');
}

const utf8 = new TextEncoder();

term.onData((data) => {
  if (!port) {
    if (data === '\r') connect();
    return;
  }
  port.postMessage({ t: 'in', d: toBase64(utf8.encode(data)) });
});

// Some mouse reports arrive as binary strings, one char per byte, which is
// what btoa takes.
term.onBinary((data) => port?.postMessage({ t: 'in', d: btoa(data) }));

term.onResize(({ cols, rows }) => port?.postMessage({ t: 'resize', cols, rows }));

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

connect();
