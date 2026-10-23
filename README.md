# paneterm

A local terminal in Chrome's side panel. Click the toolbar icon and your
login shell opens next to the page you're on.

```
side panel (xterm.js)  ⇄  native messaging  ⇄  paneterm-host  ⇄  PTY  ⇄  $SHELL -l
```

A Chrome extension can't start processes, so paneterm comes in two parts:
the extension draws the terminal, and `paneterm-host`, a small Go program,
runs the shell. Chrome starts one host per panel and talks to it over
[native messaging](https://developer.chrome.com/docs/extensions/develop/concepts/native-messaging).

Status: early. Built for macOS; also runs on Linux.

## Install

You need Go 1.22 or later and Google Chrome 116 or later.

```sh
git clone https://github.com/Kwangseok-Seo/paneterm
cd paneterm
go install ./cmd/paneterm-host     # puts paneterm-host in $(go env GOPATH)/bin
paneterm-host install              # tells Chrome where the host is
```

Then load the extension:

1. Open `chrome://extensions` and turn on **Developer mode**.
2. Click **Load unpacked** and pick the `extension` folder.
3. Pin paneterm to the toolbar and click it.

`paneterm-host install` records the binary's absolute path. If you move or
rebuild it somewhere else, run `install` again. To remove the registration,
run `paneterm-host uninstall`.

## Using it

- Each panel runs its own shell. Closing the panel hangs up that shell, the
  same as closing a terminal window.
- When the shell exits, press Enter to start a new one.
- If the panel says `Specified native messaging host not found`, run
  `paneterm-host install`.

## Security

The extension can run any command as you. That is the point, so it is
worth knowing what can reach it:

- **Only this extension can start the host.** The host manifest's
  `allowed_origins` lists one extension ID, which is fixed by the `key` in
  `extension/manifest.json`.
- **Web pages can't talk to the extension.** It declares no
  `externally_connectable` and injects no content scripts.
- **Nothing listens on the network.** The host speaks only over the pipes
  Chrome gives it, unlike terminals served from a localhost port.

## Limitations

- Google Chrome only. Other Chromium browsers (Arc, Brave, Edge) read host
  manifests from their own folders, which `install` doesn't write yet.
- No tabs, splits, or sessions that survive closing the panel.
- Some company policies block native messaging hosts
  (`NativeMessagingBlocklist`) or unpacked extensions. Check
  `chrome://policy` if the panel can't connect.

## Development

```sh
go test ./...      # PTY tests need macOS or Linux
e2e/run.sh         # needs Docker: drives the extension in Chromium
```

The e2e run loads the extension into Chromium on Linux, registers the host
with `paneterm-host install`, and checks commands, non-ASCII text, resizing,
Ctrl-C, bulk output, exit and restart, cleanup after closing panels, and
that a web page has no way to reach the extension.

The protocol between panel and host is documented on `session.Message`.

## License

MIT. The vendored [xterm.js](https://xtermjs.org) files under
`extension/vendor/xterm` are also MIT; see the license files there.
