package config

import (
	"fmt"
	"strings"
)

// ParseNetworkMap parses "mainnet=value,testnet=value" into a map keyed by
// network. It is used by multi-network serve for --db and --rpc-url. Values
// must not contain commas.
func ParseNetworkMap(value string) (map[string]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("empty network assignment")
	}
	out := make(map[string]string, 2)
	for _, part := range strings.Split(value, ",") {
		name, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			return nil, fmt.Errorf("entry %q: want network=value", part)
		}
		name, v = strings.TrimSpace(name), strings.TrimSpace(v)
		if _, err := NetworkByName(name); err != nil {
			return nil, err
		}
		if v == "" {
			return nil, fmt.Errorf("network %s: empty value", name)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("network %s named more than once", name)
		}
		out[name] = v
	}
	return out, nil
}

// IsNetworkMap reports whether a flag value uses network=value syntax.
func IsNetworkMap(value string) bool {
	return strings.Contains(value, "=")
}
