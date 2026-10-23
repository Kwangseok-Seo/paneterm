// Package paneterm carries the extension manifest into the host binary, so
// the installer can read the extension's ID from the manifest instead of
// keeping a second copy of it.
package paneterm

import _ "embed"

//go:embed extension/manifest.json
var ExtensionManifest []byte
