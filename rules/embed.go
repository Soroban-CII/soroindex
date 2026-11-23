// Package rules embeds the interface rule files (sep-NNNN.json) and their
// JSON Schema, so the sep47idx binary carries the rules it was built with.
// Commands accept --rules <dir> to load a different set instead.
package rules

import "embed"

// FS holds rules/sep-*.json, rules/schema.json, and the historical
// versions in rules/history/sep-NNNN-vX.Y.Z.json. Only the top-level files
// are matched and stored; history is used to report which version of a
// SEP a Wasm's interface implements (match.LoadVersions).
//
//go:embed sep-*.json schema.json history/*.json
var FS embed.FS
