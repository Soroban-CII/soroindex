// Package migrations embeds the numbered SQL migrations (NNNN_name.sql) that
// internal/store applies in order. A migration is never edited after it
// ships; changes go in a new, higher-numbered file.
package migrations

import "embed"

// FS holds every *.sql migration.
//
//go:embed *.sql
var FS embed.FS
