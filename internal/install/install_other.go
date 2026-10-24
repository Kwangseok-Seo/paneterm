//go:build !windows

package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// manifestDir is where Chrome looks for a user's host manifests: the
// NativeMessagingHosts folder of its default profile directory.
func manifestDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Google", "Chrome", "NativeMessagingHosts"), nil
	case "linux":
		return filepath.Join(home, ".config", "google-chrome", "NativeMessagingHosts"), nil
	}
	return "", fmt.Errorf("install supports macOS, Linux, and Windows, not %s", runtime.GOOS)
}

// Chrome on macOS and Linux finds host manifests by their folder alone.
func register(manifest string) error { return nil }

func unregister(manifest string) error { return os.Remove(manifest) }
