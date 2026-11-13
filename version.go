// Package workbuddy exposes the plugin's product identity.
package workbuddy

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var versionFile string

// Version returns the product release version embedded from VERSION.
func Version() string {
	return strings.TrimSpace(versionFile)
}
