# paneterm

## Language

This repository is public. Everything committed is English: Markdown, code
comments, identifiers, commit messages, CLI output. Conversation with the
maintainer may be in Korean; that never reaches the files.

## Layout

- `extension/`: the MV3 extension. Plain JS, no build step. xterm.js is
  vendored under `extension/vendor/xterm` (versions in `VERSIONS`), since
  MV3 forbids remote code.
- `cmd/paneterm-host`: the native host. Chrome passes the caller's origin
  as the first argument; anything else is a subcommand.
- `internal/nm`: native messaging framing only.
- `internal/session`: one PTY session per host process (a ConPTY running
  PowerShell on Windows). The panel/host protocol is documented on
  `session.Message`.
- `internal/agent`: on macOS, the LaunchAgent that runs sessions outside
  Chrome's process tree, and the relay that hands it Chrome's pipes
  (ADR-0002). Unix only.
- `internal/install`: writes the host manifest Chrome reads, on Windows
  the registry key that points to it, and on macOS the agent's plist.
- `e2e/`: drives the real extension in Chromium on Linux, in Docker.
- Decisions: `docs/adr/`.

## Conventions

- The extension ID is derived from `key` in `extension/manifest.json`
  (`install.ExtensionID`); never write the ID down anywhere else. The one
  exception is `TestExtensionIDOfShippedManifest`, which pins it to a value
  computed outside Go; changing the key means updating that test. The
  private key (`extension-key.pem`) is gitignored and stays out of the repo.
- The host name appears in Go (`install.HostName`) and in
  `extension/sidepanel.js`; `TestPanelConnectsToHostName` keeps them in step.
- Checks against shell output must look for something the shell computes
  (`$((6*7))` → `42`), never text that also appears in the echoed command.

## Testing

- `go test ./...` everywhere. `internal/session` tests are per OS (Unix PTY
  with sh, Windows ConPTY with PowerShell). On Windows, also cross-compile
  the Unix test binaries (`agent`, `install`, `session`) and run them in a
  Linux container, from their package folder with the repo mounted:
  `TestPanelConnectsToHostName` reads `extension/sidepanel.js`.
- The Windows install tests write to `HKCU\Software\paneterm-test` and
  delete it; they never touch Chrome's real host key. The others replace
  `launchctl`, so no test loads a real LaunchAgent; that, and the agent
  escaping quarantine, can only be checked on a Mac.
- `e2e/run.sh` before any change to the extension or the protocol.
