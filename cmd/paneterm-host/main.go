// Command paneterm-host is the native half of paneterm. Chrome starts it
// when a tab in the side panel connects, and it runs a shell on a PTY for
// that tab.
//
//	paneterm-host install     register with Chrome for the current user
//	paneterm-host uninstall   remove the registration
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/Kwangseok-Seo/paneterm"
	"github.com/Kwangseok-Seo/paneterm/internal/install"
	"github.com/Kwangseok-Seo/paneterm/internal/session"
)

const usage = `usage:
  paneterm-host install     register with Chrome for the current user
  paneterm-host uninstall   remove the registration`

func main() {
	// Chrome passes the caller's origin as the first argument.
	if len(os.Args) > 1 && strings.HasPrefix(os.Args[1], "chrome-extension://") {
		if err := session.Run(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, "paneterm-host:", err)
			os.Exit(1)
		}
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
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
}

func exitOn(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "paneterm-host:", err)
		os.Exit(1)
	}
}
