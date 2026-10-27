# Native messaging, not a localhost server

The panel reaches the shell through Chrome native messaging rather than a
terminal server on a localhost port (the ttyd/gotty pattern), because a
listening port is open to every web page and local process and would need
its own authentication, while a native messaging host can be started only by
the extension IDs in its manifest's `allowed_origins`. We accept a per-OS
install step (the host binary plus a manifest file in Chrome's folder), one
host process per panel, and Chrome's 1 MB cap on each message from the host.

Amended 2026-10-24, with Windows support: there the install step writes the
manifest to a folder of paneterm's own, plus a registry key under
`HKEY_CURRENT_USER` that points Chrome to it. The decision is unchanged.

Amended 2026-10-27, with tabs: each tab in the panel opens its own port, so
the cost is one host process per tab rather than per panel. The decision is
unchanged.
