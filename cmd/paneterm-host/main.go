// Command paneterm-host is the native half of paneterm. Chrome starts it
// when a tab in the side panel connects, and it runs a shell on a PTY for
// that tab. On macOS it hands the tab to the agent instead, which runs the
// shell outside Chrome's process tree (docs/adr/0002).
//
//	paneterm-host install     register with Chrome for the current user
//	paneterm-host uninstall   remove the registration
//	paneterm-host agent       the agent, which launchd keeps running on macOS
//	paneterm-host serve       one tab's shell, which the agent starts
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Kwangseok-Seo/paneterm"
	"github.com/Kwangseok-Seo/paneterm/internal/agent"
	"github.com/Kwangseok-Seo/paneterm/internal/install"
	"github.com/Kwangseok-Seo/paneterm/internal/session"
)

const usage = `usage:
  paneterm-host install     register with Chrome for the current user
  paneterm-host uninstall   remove the registration`

func main() {
	// Chrome passes the caller's origin as the first argument.
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "chrome-extension://") {
		exitOn(serveChrome())
		return
	}
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "install":
		path, id, err := install.Install(paneterm.ExtensionManifest)
		exitOn(err)
		fmt.Printf("wrote %s\nallowed extension: %s\n", path, id)
	case "uninstall":
		path, err := install.Uninstall()
		exitOn(err)
		fmt.Printf("removed %s\n", path)
	case "agent":
		exitOn(runAgent())
	case "serve":
		exitOn(session.Run(os.Stdin, os.Stdout))
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

// serveChrome serves the tab Chrome started this process for.
func serveChrome() error {
	if !agent.Needed {
		return session.Run(os.Stdin, os.Stdout)
	}
	sock, err := agent.Path()
	if err != nil {
		return err
	}
	return agent.Relay(sock, os.Environ(), os.Stdin, os.Stdout, os.Stderr)
}

// runAgent serves relays until the agent's socket fails, starting this
// executable's serve for each.
func runAgent() error {
	sock, err := agent.Path()
	if err != nil {
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	l, err := agent.Listen(sock)
	if err != nil {
		return err
	}
	return agent.Serve(l, func() *exec.Cmd { return exec.Command(exe, "serve") })
}

func exitOn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "paneterm-host:", err)
		os.Exit(1)
	}
}
