// Package api holds the v4 OpenAPI spec of Shortcut.
package api

import _ "embed"

// Spec is shortcut.openapi.json.
//
//go:embed shortcut.openapi.json
var Spec []byte
