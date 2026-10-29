//go:build !windows

package install

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/Kwangseok-Seo/paneterm"
)

// isolate points Install at a fresh home folder, skipping where manifestPath
// knows no place for the manifest, and keeps the real launchd out.
func isolate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", "") // so the agent's socket is under HOME too
	recordLaunchctl(t, notLoaded)
	if _, err := manifestPath(); err != nil {
		t.Skip(err)
	}
}

// recordLaunchctl replaces launchctl with one that records its calls and
// answers as answer does.
func recordLaunchctl(t *testing.T, answer func(args []string) error) *[][]string {
	calls := new([][]string)
	real := launchctl
	launchctl = func(args ...string) error {
		*calls = append(*calls, args)
		return answer(args)
	}
	t.Cleanup(func() { launchctl = real })
	return calls
}

// notLoaded answers as launchctl does when no agent is loaded.
func notLoaded(args []string) error {
	switch args[0] {
	case "bootout":
		return errors.New("Boot-out failed: 3: No such process")
	case "print":
		return errors.New("Could not find service")
	}
	return nil
}

// useAgent sets up the agent as on macOS, whatever this system is.
func useAgent(t *testing.T) {
	was := usesAgent
	usesAgent = true
	t.Cleanup(func() { usesAgent = was })
}

func TestInstallStartsAgent(t *testing.T) {
	isolate(t)
	useAgent(t)
	calls := recordLaunchctl(t, notLoaded) // as on a first install
	if _, _, err := Install(paneterm.ExtensionManifest); err != nil {
		t.Fatal(err)
	}
	plist := filepath.Join(os.Getenv("HOME"), "Library", "LaunchAgents", HostName+".plist")
	b, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	exe, _ = filepath.EvalSymlinks(exe)
	if args := plistStrings(t, b); len(args) < 3 || args[1] != exe || args[2] != "agent" {
		t.Fatalf("plist strings = %q, want the label, then %s agent", args, exe)
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	service := domain + "/" + HostName
	want := [][]string{{"bootout", service}, {"print", service}, {"bootstrap", domain, plist}}
	if !slices.EqualFunc(*calls, want, slices.Equal) {
		t.Fatalf("launchctl calls = %q, want %q", *calls, want)
	}

	*calls = nil
	if _, err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plist); !os.IsNotExist(err) {
		t.Fatalf("plist still there after Uninstall: %v", err)
	}
	if want := [][]string{{"bootout", service}}; !slices.EqualFunc(*calls, want, slices.Equal) {
		t.Fatalf("launchctl calls = %q, want %q", *calls, want)
	}
}

// bootout may return while the old agent is still loaded, and bootstrap
// would fail then.
func TestInstallWaitsForOldAgent(t *testing.T) {
	isolate(t)
	useAgent(t)
	loaded := 2
	calls := recordLaunchctl(t, func(args []string) error {
		if args[0] == "print" && loaded > 0 {
			loaded--
			return nil
		}
		return notLoaded(args)
	})
	if _, _, err := Install(paneterm.ExtensionManifest); err != nil {
		t.Fatal(err)
	}
	var verbs []string
	for _, c := range *calls {
		verbs = append(verbs, c[0])
	}
	if want := []string{"bootout", "print", "print", "print", "bootstrap"}; !slices.Equal(verbs, want) {
		t.Fatalf("launchctl calls = %q, want %q", verbs, want)
	}

	// One that never goes fails the install, rather than bootstrap.
	loaded = 1 << 30
	*calls = nil
	if _, _, err := Install(paneterm.ExtensionManifest); err == nil || !strings.Contains(err.Error(), "still loaded") {
		t.Fatalf("Install = %v, want it to say the old agent is still loaded", err)
	}
	if last := (*calls)[len(*calls)-1][0]; last != "print" {
		t.Fatalf("last launchctl call = %q, want no bootstrap", last)
	}
}

func TestFailedAgentStartFailsInstall(t *testing.T) {
	isolate(t)
	useAgent(t)
	recordLaunchctl(t, func(args []string) error {
		if args[0] == "bootstrap" {
			return errors.New("Bootstrap failed: 5: Input/output error")
		}
		return notLoaded(args)
	})
	if _, _, err := Install(paneterm.ExtensionManifest); err == nil || !strings.Contains(err.Error(), "Bootstrap failed") {
		t.Fatalf("Install = %v, want the bootstrap failure", err)
	}
}

// A path XML would misread still reaches launchd as it is.
func TestAgentPlistEscapes(t *testing.T) {
	exe := "/Users/a&b/<go>/bin/paneterm-host"
	if args := plistStrings(t, agentPlist(exe, "/tmp/log")); len(args) < 2 || args[1] != exe {
		t.Fatalf("plist strings = %q, want %q second", args, exe)
	}
}

// plistStrings decodes plist and returns its <string> values in order.
func plistStrings(t *testing.T, plist []byte) []string {
	t.Helper()
	var doc struct {
		Strings []string `xml:"dict>string"`
		Args    []string `xml:"dict>array>string"`
	}
	if err := xml.Unmarshal(plist, &doc); err != nil || len(doc.Strings) == 0 {
		t.Fatalf("plist does not parse as one (%v):\n%s", err, plist)
	}
	// Label, ProgramArguments, StandardErrorPath, in document order.
	return append(append(doc.Strings[:1:1], doc.Args...), doc.Strings[1:]...)
}
