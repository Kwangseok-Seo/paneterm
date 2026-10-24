//go:build !windows

package install

import "testing"

// isolate points Install at a fresh home folder, skipping where manifestPath
// knows no place for the manifest.
func isolate(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := manifestPath(); err != nil {
		t.Skip(err)
	}
}
