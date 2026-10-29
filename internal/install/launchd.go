//go:build !windows

package install

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Kwangseok-Seo/paneterm/internal/agent"
)

// usesAgent says whether install sets up the agent that runs the shells
// (docs/adr/0002). Tests turn it on elsewhere too, with launchctl replaced.
var usesAgent = agent.Needed

// launchctl runs launchctl. Tests replace it to keep the real launchd out.
var launchctl = func(args ...string) error {
	out, err := exec.Command("launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("launchctl %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}

// plistFormat is the LaunchAgent that keeps the agent running. Without a
// ProcessType, launchd throttles the CPU and I/O of the job, the shells
// included; Interactive is what apps get. With AbandonProcessGroup, shells
// the agent started outlive it when it restarts, as it does on every
// install.
const plistFormat = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>agent</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>AbandonProcessGroup</key>
	<true/>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`

func agentPlist(exe, log string) []byte {
	esc := func(s string) string {
		var b strings.Builder
		xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	return fmt.Appendf(nil, plistFormat, esc(HostName), esc(exe), esc(log))
}

// agentFiles are where the agent's plist and log go.
func agentFiles() (plist, log string, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	lib := filepath.Join(home, "Library")
	return filepath.Join(lib, "LaunchAgents", HostName+".plist"), filepath.Join(lib, "Logs", "paneterm-agent.log"), nil
}

// agentService names the agent to launchctl, in the user's GUI session.
func agentService() (domain, service string) {
	domain = fmt.Sprintf("gui/%d", os.Getuid())
	return domain, domain + "/" + HostName
}

// startAgent has launchd run exe's agent, now and at every login, in place
// of any agent an earlier install started.
func startAgent(exe string) error {
	plist, log, err := agentFiles()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(plist), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plist, agentPlist(exe, log), 0o644); err != nil {
		return err
	}
	domain, service := agentService()
	// Fails when no agent is loaded yet, and may return before the old
	// agent is gone, while bootstrap would fail.
	stopped := launchctl("bootout", service)
	for wait := 0; launchctl("print", service) == nil; wait++ {
		if wait == 30 {
			return fmt.Errorf("the agent an earlier install started is still loaded (%v)", stopped)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return launchctl("bootstrap", domain, plist)
}

// stopAgent undoes startAgent, and removes the socket and the log the
// agent leaves behind.
func stopAgent() error {
	plist, log, err := agentFiles()
	if err != nil {
		return err
	}
	_, service := agentService()
	launchctl("bootout", service) // fails when the agent isn't loaded
	if sock, err := agent.Path(); err == nil {
		os.Remove(sock)
		os.Remove(filepath.Dir(sock)) // empty now, unless someone put more there
	}
	os.Remove(log)
	if err := os.Remove(plist); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
