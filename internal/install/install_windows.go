//go:build windows

package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// hostKey is the key under HKEY_CURRENT_USER whose default value tells
// Chrome on Windows where a host's manifest is. Tests point it elsewhere.
var hostKey = `Software\Google\Chrome\NativeMessagingHosts\` + HostName

// manifestDir is a folder of paneterm's own: Chrome on Windows finds the
// manifest through hostKey, wherever it is.
func manifestDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "paneterm"), nil
}

func register(manifest, exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, hostKey, registry.SET_VALUE)
	if err != nil {
		return keyError(err)
	}
	defer k.Close()
	return keyError(k.SetStringValue("", manifest))
}

// unregister removes the key, the manifest, and the folder made for it.
func unregister(manifest string) error {
	err := errors.Join(keyError(registry.DeleteKey(registry.CURRENT_USER, hostKey)), os.Remove(manifest))
	os.Remove(filepath.Dir(manifest)) // empty now, unless someone put more there
	return err
}

// keyError names the key, which the registry's own errors leave out.
func keyError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(`HKEY_CURRENT_USER\%s: %w`, hostKey, err)
}
