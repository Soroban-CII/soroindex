package config

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/Soroban-CII/soroindex/internal/rpc"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// DefaultMaxXDRBase64Bytes is the default ceiling on one base64 XDR string.
// It is the value ingest.DefaultXDRBase64Len has, repeated here so this
// package stays free of ingest's dependencies; a test asserts the two agree.
const DefaultMaxXDRBase64Bytes = 64 << 20

// MaxConfigurableXDRBase64Bytes caps --max-xdr-base64-bytes at the largest
// response body the RPC client reads (rpc.DefaultMaxResponseBytes). A single
// base64 field cannot be longer than the whole response that carries it, so
// a larger value could never be reached and would only raise the amount of
// memory one hostile response is allowed to ask for.
const MaxConfigurableXDRBase64Bytes = int(rpc.DefaultMaxResponseBytes)

// Limits are the parser and XDR bounds an operator may tune (CLAUDE.md
// §5.1). They are flat and JSON-tagged because the effective values are
// recorded in sync_state under the "limits" key, where they document what
// the stored data was parsed with.
type Limits struct {
	MaxWasmBytes      int  `json:"max_wasm_bytes"`
	MaxSectionBytes   int  `json:"max_section_bytes"`
	MaxSections       int  `json:"max_sections"`
	MaxXDRDepth       uint `json:"max_xdr_depth"`
	MaxXDRBase64Bytes int  `json:"max_xdr_base64_bytes"`
}

// DefaultLimits are the shipped limits: the sepmeta defaults plus the XDR
// string ceiling. A deployment that sets no limit flag runs with exactly
// these, which is what every release before them used.
func DefaultLimits() Limits {
	d := sepmeta.DefaultLimits()
	return Limits{
		MaxWasmBytes:      d.MaxWasmBytes,
		MaxSectionBytes:   d.MaxSectionBytes,
		MaxSections:       d.MaxSections,
		MaxXDRDepth:       d.MaxXDRDepth,
		MaxXDRBase64Bytes: DefaultMaxXDRBase64Bytes,
	}
}

// Sepmeta returns the parser limits, for pkg/sepmeta and for every package
// that parses Wasm through it.
func (l Limits) Sepmeta() sepmeta.Limits {
	return sepmeta.Limits{
		MaxWasmBytes:    l.MaxWasmBytes,
		MaxSectionBytes: l.MaxSectionBytes,
		MaxSections:     l.MaxSections,
		MaxXDRDepth:     l.MaxXDRDepth,
	}
}

// limitFlagName maps a sepmeta.Limits field name to the flag that sets it,
// so a validation failure names the flag the operator typed rather than a Go
// field they have never seen.
var limitFlagName = map[string]string{
	"MaxWasmBytes":    "--max-wasm-bytes",
	"MaxSectionBytes": "--max-section-bytes",
	"MaxSections":     "--max-sections",
	"MaxXDRDepth":     "--max-xdr-depth",
}

// Validate rejects a limit that is below one or above its hard cap. It runs
// at startup, before any Wasm or XDR is read, so a bad value is a refusal to
// start rather than a failure part-way through an ingest.
func (l Limits) Validate() error {
	var rangeErr *sepmeta.LimitRangeError
	if err := l.Sepmeta().Validate(); err != nil {
		if errors.As(err, &rangeErr) {
			if name, ok := limitFlagName[rangeErr.Limit]; ok {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
		return err
	}
	switch {
	case l.MaxXDRBase64Bytes < 1:
		return fmt.Errorf("--max-xdr-base64-bytes: %w: is %d, want at least 1", sepmeta.ErrInvalidLimit, l.MaxXDRBase64Bytes)
	case l.MaxXDRBase64Bytes > MaxConfigurableXDRBase64Bytes:
		return fmt.Errorf("--max-xdr-base64-bytes: %w: is %d, want at most %d, the largest RPC response body the client reads",
			sepmeta.ErrInvalidLimit, l.MaxXDRBase64Bytes, MaxConfigurableXDRBase64Bytes)
	}
	return nil
}

// JSON is the canonical sync_state value for these limits.
func (l Limits) JSON() (string, error) {
	b, err := json.Marshal(l)
	if err != nil {
		return "", fmt.Errorf("encode limits: %w", err)
	}
	return string(b), nil
}

// ParseLimitsJSON reads a value written by JSON. A value that does not
// decode, or that leaves a limit unset, is an error rather than a silent
// fallback to the defaults: the point of recording the limits is to say what
// the stored data was actually parsed with. Unknown fields are ignored, so a
// database written by a later binary can still be read.
func ParseLimitsJSON(s string) (Limits, error) {
	var l Limits
	if err := json.Unmarshal([]byte(s), &l); err != nil {
		return Limits{}, fmt.Errorf("decode limits %q: %w", s, err)
	}
	if l.MaxWasmBytes < 1 || l.MaxSectionBytes < 1 || l.MaxSections < 1 || l.MaxXDRDepth < 1 || l.MaxXDRBase64Bytes < 1 {
		return Limits{}, fmt.Errorf("decode limits %q: a recorded limit is missing or not positive", s)
	}
	return l, nil
}

// String is the one-line summary the stats command prints.
func (l Limits) String() string {
	return fmt.Sprintf("wasm %s, section %s, sections %d, xdr depth %d, xdr base64 %s",
		ByteSize(l.MaxWasmBytes), ByteSize(l.MaxSectionBytes), l.MaxSections, l.MaxXDRDepth, ByteSize(l.MaxXDRBase64Bytes))
}

// RegisterLimits adds the limit flags to fs and returns a function that
// resolves and validates them after fs.Parse. It is registered only by
// commands that parse Wasm or XDR, so a read-only command's help stays
// short.
func RegisterLimits(fs *flag.FlagSet) func() (Limits, error) {
	d := DefaultLimits()
	wasmBytes, sectionBytes := ByteSize(d.MaxWasmBytes), ByteSize(d.MaxSectionBytes)
	xdrBytes := ByteSize(d.MaxXDRBase64Bytes)
	fs.Var(&wasmBytes, "max-wasm-bytes", "largest Wasm module parsed, bytes or a size such as 4MiB (env "+EnvName("max-wasm-bytes")+
		"; hard cap "+ByteSize(sepmeta.MaxConfigurableWasmBytes).String()+")")
	fs.Var(&sectionBytes, "max-section-bytes", "largest custom section parsed, after joining repeats (env "+EnvName("max-section-bytes")+
		"; must not exceed --max-wasm-bytes)")
	sections := fs.Int("max-sections", d.MaxSections, "most Wasm sections walked in one module (env "+EnvName("max-sections")+
		"; must not exceed --max-wasm-bytes/2)")
	depth := fs.Uint("max-xdr-depth", d.MaxXDRDepth, "XDR nesting depth accepted (env "+EnvName("max-xdr-depth")+
		"; hard cap "+strconv.Itoa(sepmeta.MaxConfigurableXDRDepth)+")")
	fs.Var(&xdrBytes, "max-xdr-base64-bytes", "largest base64 XDR string decoded, which bounds ledger metadata (env "+
		EnvName("max-xdr-base64-bytes")+"; hard cap "+ByteSize(MaxConfigurableXDRBase64Bytes).String()+")")
	return func() (Limits, error) {
		// ApplyEnv is idempotent: it skips flags already set, including by
		// an earlier resolver on the same FlagSet, so the order in which a
		// command calls its resolvers does not matter.
		if err := ApplyEnv(fs, os.LookupEnv); err != nil {
			return Limits{}, err
		}
		l := Limits{
			MaxWasmBytes:      int(wasmBytes),
			MaxSectionBytes:   int(sectionBytes),
			MaxSections:       *sections,
			MaxXDRDepth:       *depth,
			MaxXDRBase64Bytes: int(xdrBytes),
		}
		if err := l.Validate(); err != nil {
			return Limits{}, err
		}
		return l, nil
	}
}

// ByteSize is a flag value in bytes that also accepts a suffixed size, so an
// operator can write 4MiB instead of counting zeros. It is a flag.Value, so
// an unparseable value fails at flag parsing, before any work starts.
type ByteSize int

// byteSizeUnits are the accepted suffixes, longest first so that "MiB"
// matches before "B". Decimal units are accepted because operators read them
// on hardware and in RPC provider documentation.
var byteSizeUnits = []struct {
	suffix string
	scale  int64
}{
	{"KiB", 1 << 10}, {"MiB", 1 << 20}, {"GiB", 1 << 30},
	{"KB", 1000}, {"MB", 1000 * 1000}, {"GB", 1000 * 1000 * 1000},
	{"K", 1 << 10}, {"M", 1 << 20}, {"G", 1 << 30},
	{"B", 1},
}

// Set implements flag.Value. It rejects anything that is not a positive byte
// count: an empty string, a negative number, a bare suffix, a fraction, or a
// value that would overflow an int.
func (b *ByteSize) Set(v string) error {
	s := strings.TrimSpace(v)
	if s == "" {
		return errors.New("empty size")
	}
	scale := int64(1)
	for _, u := range byteSizeUnits {
		if len(s) > len(u.suffix) && strings.EqualFold(s[len(s)-len(u.suffix):], u.suffix) {
			scale = u.scale
			s = strings.TrimSpace(s[:len(s)-len(u.suffix)])
			break
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("size %q: want a byte count, optionally suffixed B, KiB, MiB, GiB, KB, MB or GB", v)
	}
	if n < 1 {
		return fmt.Errorf("size %q: want at least 1 byte", v)
	}
	if n > (1<<62)/scale {
		return fmt.Errorf("size %q: too large", v)
	}
	n *= scale
	if int64(int(n)) != n {
		return fmt.Errorf("size %q: too large for this platform", v)
	}
	*b = ByteSize(n)
	return nil
}

// String implements flag.Value, printing a whole binary unit where the value
// is one, so --help shows "1MiB" rather than "1048576".
func (b ByteSize) String() string {
	n := int64(b)
	if n <= 0 {
		return strconv.FormatInt(n, 10)
	}
	for _, u := range []struct {
		suffix string
		scale  int64
	}{{"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if n%u.scale == 0 {
			return strconv.FormatInt(n/u.scale, 10) + u.suffix
		}
	}
	return strconv.FormatInt(n, 10)
}
