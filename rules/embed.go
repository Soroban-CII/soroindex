// Package rules embeds the interface rule files (sep-NNNN.json) and their
// JSON Schema, so the sep47idx binary carries the rules it was built with.
// Commands accept --rules <dir> to load a different set instead.
package rules

import "embed"

// FS holds rules/sep-*.json and rules/schema.json.
//
//go:embed sep-*.json schema.json
var FS embed.FS
