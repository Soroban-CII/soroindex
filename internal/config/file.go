package config

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
)

// secretFlags names flags that must not come from a config file.
// RPC authentication, when added, stays env-only so operators cannot
// accidentally commit credentials in a versioned config file.
var secretFlags = map[string]bool{
	// Currently empty: no authentication flags exist yet.
	// Add entries here (e.g. "rpc-auth-token") when authentication flags
	// are introduced; they will then be accepted only from the environment
	// or the command line, never from a file.
}

// ApplyFile reads a JSON config file at path and sets each flag from it,
// unless the flag was already set on the command line. Unknown JSON keys
// are errors that name the key. Flags named in secretFlags are silently
// skipped so secrets never come from a file.
//
// The file uses flag names as JSON keys. Values are JSON strings, booleans,
// or numbers; durations must be JSON strings (e.g. "rpc-timeout": "30s").
//
// ApplyFile must be called after fs.Parse and before ApplyEnv so that the
// final precedence is: flags > env > file > defaults.
func ApplyFile(fs *flag.FlagSet, path string) error {
	if path == "" {
		return nil
	}
	f, err := os.Open(path) // #nosec G304 -- operator-supplied config path
	if err != nil {
		return fmt.Errorf("config file: %w", err)
	}
	defer func() { _ = f.Close() }()

	var raw map[string]json.RawMessage
	if err := json.NewDecoder(f).Decode(&raw); err != nil {
		return fmt.Errorf("config file %s: %w", path, err)
	}

	// Build the set of flags the FlagSet knows about.
	known := map[string]bool{}
	fs.VisitAll(func(fl *flag.Flag) { known[fl.Name] = true })

	// Validate every key before applying any, so the file is accepted or
	// rejected atomically. The "config" key is silently allowed but ignored
	// to prevent errors on a file that names its own path.
	for k := range raw {
		if k == "config" {
			continue
		}
		if !known[k] {
			return fmt.Errorf("config file %s: unknown key %q", path, k)
		}
	}

	// Build the set of flags already set on the command line.
	cmdline := map[string]bool{}
	fs.Visit(func(fl *flag.Flag) { cmdline[fl.Name] = true })

	for k, v := range raw {
		if k == "config" || cmdline[k] || secretFlags[k] {
			continue
		}
		s, err := jsonToFlagString(v)
		if err != nil {
			return fmt.Errorf("config file %s: key %q: %w", path, k, err)
		}
		if err := fs.Set(k, s); err != nil {
			return fmt.Errorf("config file %s: key %q: %w", path, k, err)
		}
	}
	return nil
}

// jsonToFlagString converts a JSON value to the string accepted by flag.Set.
// Strings pass through unchanged; booleans become "true" or "false"; numbers
// use their raw JSON text so integers are not rounded through float64.
func jsonToFlagString(raw json.RawMessage) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s, nil
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		if b {
			return "true", nil
		}
		return "false", nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err == nil {
		return n.String(), nil
	}
	return "", errors.New("value must be a JSON string, boolean, or number")
}

