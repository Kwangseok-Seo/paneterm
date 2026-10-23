// Package install registers paneterm-host with Chrome as a native
// messaging host for the current user.
package install

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// HostName is the name the extension passes to chrome.runtime.connectNative.
const HostName = "io.github.kwangseok_seo.paneterm"

// ExtensionID derives an extension's ID from the "key" in its manifest the
// way Chrome does: the first 128 bits of the SHA-256 of the public key, in
// hex, with the digits 0-f written as a-p.
func ExtensionID(manifest []byte) (string, error) {
	var m struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(manifest, &m); err != nil {
		return "", err
	}
	if m.Key == "" {
		return "", errors.New(`extension manifest has no "key"`)
	}
	der, err := base64.StdEncoding.DecodeString(m.Key)
	if err != nil {
		return "", fmt.Errorf("extension manifest key: %w", err)
	}
	sum := sha256.Sum256(der)
	id := make([]byte, 32)
	for i, b := range sum[:16] {
		id[2*i] = 'a' + b>>4
		id[2*i+1] = 'a' + b&0xf
	}
	return string(id), nil
}

// Install writes the host manifest that lets the extension, and only it,
// start this executable. It returns the manifest's path and the ID it
// allowed.
func Install(extensionManifest []byte) (path, id string, err error) {
	if path, err = manifestPath(); err != nil {
		return "", "", err
	}
	if id, err = ExtensionID(extensionManifest); err != nil {
		return "", "", err
	}
	exe, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", "", err
	}
	host, err := json.MarshalIndent(map[string]any{
		"name":            HostName,
		"description":     "paneterm terminal host",
		"path":            exe,
		"type":            "stdio",
		"allowed_origins": []string{"chrome-extension://" + id + "/"},
	}, "", "  ")
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", "", err
	}
	return path, id, os.WriteFile(path, append(host, '\n'), 0o644)
}

// Uninstall removes the host manifest. It returns the path it removed.
func Uninstall() (string, error) {
	path, err := manifestPath()
	if err != nil {
		return "", err
	}
	return path, os.Remove(path)
}

// manifestPath is where Chrome looks for a user's host manifests: the
// NativeMessagingHosts folder of its default profile directory.
func manifestPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	var dir string
	switch runtime.GOOS {
	case "darwin":
		dir = filepath.Join(home, "Library", "Application Support", "Google", "Chrome")
	case "linux":
		dir = filepath.Join(home, ".config", "google-chrome")
	default:
		return "", fmt.Errorf("install supports macOS and Linux, not %s", runtime.GOOS)
	}
	return filepath.Join(dir, "NativeMessagingHosts", HostName+".json"), nil
}
