# On macOS, shells run under a LaunchAgent

On macOS the host that Chrome starts does not run the shell: it hands
Chrome's pipes to an agent that launchd keeps running (`agent.Relay`,
`agent.Serve`), because Chrome's `Info.plist` sets `LSFileQuarantineEnabled`,
which puts Chrome's quarantine attribute on every file that Chrome or a
process under it creates, and Gatekeeper then refuses programs built in the
shell. We accept a process that stays running for the user, an install step
that loads it with `launchctl`, and a Unix socket that any of the user's
processes can reach.

The facts this rests on were seen on a Mac: a `go build` output in the
panel carried `0081;…;Chrome;` and was killed when run, while a file that a
launchd job created, at the panel's request, carried no quarantine at all.
No API was found that lets a process drop its own quarantine. A Launch
Services helper app started per tab would also leave Chrome's tree without a
resident process, at the cost of an app bundle to install and a launch per
tab. Removing the attribute after the fact would also remove it from real
downloads.
