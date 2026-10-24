//go:build windows

package install

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows/registry"

	"github.com/Kwangseok-Seo/paneterm"
)

// isolate points Install at a fresh AppData folder and a registry key of
// its own, leaving the developer's real registration alone.
func isolate(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	saved := hostKey
	hostKey = `Software\paneterm-test\` + t.Name()
	t.Cleanup(func() {
		registry.DeleteKey(registry.CURRENT_USER, `Software\paneterm-test\`+t.Name())
		registry.DeleteKey(registry.CURRENT_USER, `Software\paneterm-test`)
		hostKey = saved
	})
}

func registered(t *testing.T) string {
	t.Helper()
	k, err := registry.OpenKey(registry.CURRENT_USER, hostKey, registry.QUERY_VALUE)
	if err != nil {
		t.Fatal(err)
	}
	defer k.Close()
	v, _, err := k.GetStringValue("")
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestInstallRegistersManifest(t *testing.T) {
	isolate(t)
	path, _, err := Install(paneterm.ExtensionManifest)
	if err != nil {
		t.Fatal(err)
	}
	if got := registered(t); got != path {
		t.Fatalf("registered %q, want %q", got, path)
	}

	if _, err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, hostKey, registry.QUERY_VALUE); !errors.Is(err, registry.ErrNotExist) {
		k.Close()
		t.Fatalf("registry key still there after Uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatalf("manifest folder still there after Uninstall: %v", err)
	}
	if _, err := Uninstall(); err == nil || !strings.Contains(err.Error(), hostKey) {
		t.Fatalf("second Uninstall = %v, want an error naming %s", err, hostKey)
	}
}

// Reinstalling rewrites the manifest an earlier install registered. If the
// registry refuses this time, that registration must still find it.
func TestFailedRegistrationKeepsManifest(t *testing.T) {
	isolate(t)
	path, _, err := Install(paneterm.ExtensionManifest)
	if err != nil {
		t.Fatal(err)
	}
	working := hostKey
	hostKey += `\` + strings.Repeat("x", 300) // longer than a key name may be
	if _, _, err := Install(paneterm.ExtensionManifest); err == nil {
		t.Fatal("Install succeeded with an invalid registry key")
	}
	hostKey = working
	if got := registered(t); got != path {
		t.Fatalf("registered %q, want %q", got, path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the earlier registration lost its manifest: %v", err)
	}
}
