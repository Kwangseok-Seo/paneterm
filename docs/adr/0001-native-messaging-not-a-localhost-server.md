# Native messaging, not a localhost server

The panel reaches the shell through Chrome native messaging rather than a
terminal server on a localhost port (the ttyd/gotty pattern), because a
listening port is open to every web page and local process and would need
its own authentication, while a native messaging host can be started only by
the extension IDs in its manifest's `allowed_origins`. We accept a per-OS
install step (the host binary plus a manifest file in Chrome's folder), one
host process per panel, and Chrome's 1 MB cap on each message from the host.
