package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"

	"github.com/Kwangseok-Seo/paneterm"
)

// The expected ID was computed outside Go from the same key:
//
//	openssl rsa -in extension-key.pem -pubout -outform DER | sha256sum | head -c32 | tr 0-9a-f a-p
func TestExtensionIDOfShippedManifest(t *testing.T) {
	id, err := ExtensionID(paneterm.ExtensionManifest)
	if err != nil {
		t.Fatal(err)
	}
	if want := "onfjaflhnleionjipfaidfppjcmlalfm"; id != want {
		t.Fatalf("ExtensionID = %s, want %s", id, want)
	}
}

func TestExtensionIDNeedsKey(t *testing.T) {
	if _, err := ExtensionID([]byte(`{"name":"x"}`)); err == nil {
		t.Fatal("ExtensionID accepted a manifest without a key")
	}
}

func TestInstallWritesHostManifest(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("install supports macOS and Linux only")
	}
	t.Setenv("HOME", t.TempDir())
	path, id, err := Install(paneterm.ExtensionManifest)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Name           string   `json:"name"`
		Path           string   `json:"path"`
		Type           string   `json:"type"`
		AllowedOrigins []string `json:"allowed_origins"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	if m.Name != HostName || m.Type != "stdio" || !filepath.IsAbs(m.Path) || filepath.Base(m.Path) != filepath.Base(exe) {
		t.Fatalf("manifest = %+v", m)
	}
	if want := []string{"chrome-extension://" + id + "/"}; len(m.AllowedOrigins) != 1 || m.AllowedOrigins[0] != want[0] {
		t.Fatalf("allowed_origins = %v, want %v", m.AllowedOrigins, want)
	}

	if _, err := Uninstall(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("manifest still there after Uninstall: %v", err)
	}
}

// The panel names the host in JavaScript; a rename here must reach it.
func TestPanelConnectsToHostName(t *testing.T) {
	js, err := os.ReadFile("../../extension/sidepanel.js")
	if err != nil {
		t.Fatal(err)
	}
	decl := regexp.MustCompile(`(?m)^const HOST = ['"]([^'"]+)['"];$`).FindSubmatch(js)
	if decl == nil || string(decl[1]) != HostName {
		t.Fatalf("sidepanel.js declares HOST as %q, want %q", decl, HostName)
	}
	if calls := regexp.MustCompile(`connectNative\(([^)]*)\)`).FindAllSubmatch(js, -1); len(calls) != 1 || string(calls[0][1]) != "HOST" {
		t.Fatalf("sidepanel.js should call connectNative(HOST) once, found %q", calls)
	}
}
