package config

import (
	"errors"
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/Soroban-CII/soroindex/internal/ingest"
	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// resolveLimits parses args with the limit flags registered and env supplied
// from a map, the way a command does.
func resolveLimits(t *testing.T, args []string, env map[string]string) (Limits, error) {
	t.Helper()
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	resolve := RegisterLimits(fs)
	if err := fs.Parse(args); err != nil {
		return Limits{}, err
	}
	if err := ApplyEnv(fs, func(k string) (string, bool) { v, ok := env[k]; return v, ok }); err != nil {
		return Limits{}, err
	}
	return resolve()
}

func TestLimitFlagsDefaultToTheShippedLimits(t *testing.T) {
	got, err := resolveLimits(t, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != DefaultLimits() {
		t.Fatalf("limits = %+v, want %+v", got, DefaultLimits())
	}
	// A deployment that sets no flag must behave exactly as before: the
	// parser limits it produces are the ones pkg/sepmeta has always used,
	// and the XDR ceiling is the one internal/ingest has always used.
	if got.Sepmeta() != sepmeta.DefaultLimits() {
		t.Fatalf("Sepmeta() = %+v, want %+v", got.Sepmeta(), sepmeta.DefaultLimits())
	}
	if got.MaxXDRBase64Bytes != ingest.DefaultXDRBase64Len {
		t.Fatalf("MaxXDRBase64Bytes = %d, want ingest.DefaultXDRBase64Len (%d)", got.MaxXDRBase64Bytes, ingest.DefaultXDRBase64Len)
	}
	if DefaultMaxXDRBase64Bytes != ingest.DefaultXDRBase64Len {
		t.Fatalf("DefaultMaxXDRBase64Bytes %d has drifted from ingest.DefaultXDRBase64Len %d", DefaultMaxXDRBase64Bytes, ingest.DefaultXDRBase64Len)
	}
}

func TestLimitFlagsAndEnv(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want Limits
	}{
		{
			name: "every limit is settable by flag, with suffixed sizes",
			args: []string{"--max-wasm-bytes", "4MiB", "--max-section-bytes", "1MiB", "--max-sections", "500", "--max-xdr-depth", "100", "--max-xdr-base64-bytes", "8MiB"},
			want: Limits{MaxWasmBytes: 4 << 20, MaxSectionBytes: 1 << 20, MaxSections: 500, MaxXDRDepth: 100, MaxXDRBase64Bytes: 8 << 20},
		},
		{
			name: "every limit is settable by environment variable",
			env: map[string]string{
				"SEP47IDX_MAX_WASM_BYTES": "2097152", "SEP47IDX_MAX_SECTION_BYTES": "512KiB",
				"SEP47IDX_MAX_SECTIONS": "99", "SEP47IDX_MAX_XDR_DEPTH": "250", "SEP47IDX_MAX_XDR_BASE64_BYTES": "32MiB",
			},
			want: Limits{MaxWasmBytes: 2 << 20, MaxSectionBytes: 512 << 10, MaxSections: 99, MaxXDRDepth: 250, MaxXDRBase64Bytes: 32 << 20},
		},
		{
			name: "a flag overrides the environment",
			args: []string{"--max-wasm-bytes", "4MiB"},
			env:  map[string]string{"SEP47IDX_MAX_WASM_BYTES": "8MiB"},
			want: Limits{MaxWasmBytes: 4 << 20, MaxSectionBytes: 256 << 10, MaxSections: 10_000, MaxXDRDepth: 500, MaxXDRBase64Bytes: DefaultMaxXDRBase64Bytes},
		},
		{
			name: "an empty environment variable does not blank a default",
			env:  map[string]string{"SEP47IDX_MAX_WASM_BYTES": ""},
			want: DefaultLimits(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveLimits(t, tt.args, tt.env)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("limits = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestLimitFlagsRejectBadValuesAtStartup(t *testing.T) {
	tests := []struct {
		name string
		args []string
		env  map[string]string
		want string // substring of the error
	}{
		{name: "zero is not treated as unlimited", args: []string{"--max-wasm-bytes", "0"}, want: "at least 1 byte"},
		{name: "a negative size is refused", args: []string{"--max-section-bytes", "-1"}, want: "at least 1 byte"},
		{name: "a non-numeric size is refused", args: []string{"--max-wasm-bytes", "big"}, want: "want a byte count"},
		{name: "an unparseable suffixed size is refused", args: []string{"--max-wasm-bytes", "1.5MiB"}, want: "want a byte count"},
		{name: "a bare suffix is refused", args: []string{"--max-wasm-bytes", "MiB"}, want: "want a byte count"},
		{name: "an empty size is refused", args: []string{"--max-wasm-bytes", ""}, want: "empty size"},
		{
			name: "a Wasm limit over the hard cap names the flag and the cap",
			args: []string{"--max-wasm-bytes", "65MiB"},
			want: "--max-wasm-bytes",
		},
		{
			name: "a section limit larger than the module is refused",
			args: []string{"--max-section-bytes", "2MiB"},
			want: "--max-section-bytes",
		},
		{
			name: "a section count a module cannot hold is refused",
			args: []string{"--max-wasm-bytes", "4KiB", "--max-section-bytes", "1KiB"},
			want: "--max-sections",
		},
		{
			name: "lowering only the module size reports the section size first, naming its flag",
			args: []string{"--max-wasm-bytes", "4KiB"},
			want: "--max-section-bytes",
		},
		{name: "a zero section count is refused", args: []string{"--max-sections", "0"}, want: "--max-sections"},
		{name: "a zero XDR depth is refused", args: []string{"--max-xdr-depth", "0"}, want: "--max-xdr-depth"},
		{name: "an XDR depth over the decoder's maximum is refused", args: []string{"--max-xdr-depth", "1501"}, want: "--max-xdr-depth"},
		{
			name: "an XDR string ceiling over the RPC response cap is refused",
			args: []string{"--max-xdr-base64-bytes", "257MiB"},
			want: "--max-xdr-base64-bytes",
		},
		{
			name: "a bad environment value is refused and names its variable",
			env:  map[string]string{"SEP47IDX_MAX_SECTIONS": "many"},
			want: "SEP47IDX_MAX_SECTIONS",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveLimits(t, tt.args, tt.env)
			if err == nil {
				t.Fatalf("accepted %v %v as %+v", tt.args, tt.env, got)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestLimitsAtTheHardCapsAreAccepted(t *testing.T) {
	got, err := resolveLimits(t, []string{
		"--max-wasm-bytes", "64MiB", "--max-section-bytes", "64MiB", "--max-sections", "33554432",
		"--max-xdr-depth", "1500", "--max-xdr-base64-bytes", "256MiB",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := Limits{MaxWasmBytes: sepmeta.MaxConfigurableWasmBytes, MaxSectionBytes: sepmeta.MaxConfigurableWasmBytes,
		MaxSections: sepmeta.MaxConfigurableWasmBytes / 2, MaxXDRDepth: sepmeta.MaxConfigurableXDRDepth,
		MaxXDRBase64Bytes: MaxConfigurableXDRBase64Bytes}
	if got != want {
		t.Fatalf("limits = %+v, want %+v", got, want)
	}
}

func TestLimitsValidateWrapsTheSentinel(t *testing.T) {
	l := DefaultLimits()
	l.MaxXDRBase64Bytes = 0
	if err := l.Validate(); !errors.Is(err, sepmeta.ErrInvalidLimit) {
		t.Fatalf("Validate() = %v, want ErrInvalidLimit", err)
	}
	l = DefaultLimits()
	l.MaxSections = 0
	if err := l.Validate(); !errors.Is(err, sepmeta.ErrInvalidLimit) {
		t.Fatalf("Validate() = %v, want ErrInvalidLimit", err)
	}
}

func TestLimitsJSONRoundTrip(t *testing.T) {
	l := Limits{MaxWasmBytes: 4 << 20, MaxSectionBytes: 1 << 20, MaxSections: 7, MaxXDRDepth: 9, MaxXDRBase64Bytes: 11 << 20}
	s, err := l.JSON()
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"max_wasm_bytes":4194304,"max_section_bytes":1048576,"max_sections":7,"max_xdr_depth":9,"max_xdr_base64_bytes":11534336}`
	if s != want {
		t.Fatalf("JSON() = %s, want %s", s, want)
	}
	back, err := ParseLimitsJSON(s)
	if err != nil || back != l {
		t.Fatalf("ParseLimitsJSON = %+v, %v; want %+v", back, err, l)
	}
}

func TestParseLimitsJSONRejectsIncompleteValues(t *testing.T) {
	for _, in := range []string{"", "{", "null", "[]", `{"max_wasm_bytes":1}`, `{"max_wasm_bytes":0,"max_section_bytes":1,"max_sections":1,"max_xdr_depth":1,"max_xdr_base64_bytes":1}`} {
		if got, err := ParseLimitsJSON(in); err == nil {
			t.Errorf("ParseLimitsJSON(%q) = %+v, want an error", in, got)
		}
	}
	t.Run("a value from a later binary with extra keys still reads", func(t *testing.T) {
		got, err := ParseLimitsJSON(`{"max_wasm_bytes":1,"max_section_bytes":1,"max_sections":1,"max_xdr_depth":1,"max_xdr_base64_bytes":1,"max_future_thing":5}`)
		if err != nil || got.MaxWasmBytes != 1 {
			t.Fatalf("got %+v, %v", got, err)
		}
	})
}

func TestByteSizeSet(t *testing.T) {
	tests := []struct {
		in      string
		want    int
		wantErr bool
	}{
		{in: "1", want: 1},
		{in: "1048576", want: 1 << 20},
		{in: " 2MiB ", want: 2 << 20},
		{in: "2mib", want: 2 << 20},
		{in: "512KiB", want: 512 << 10},
		{in: "1GiB", want: 1 << 30},
		{in: "4B", want: 4},
		{in: "2KB", want: 2000},
		{in: "3MB", want: 3_000_000},
		{in: "1GB", want: 1_000_000_000},
		{in: "2M", want: 2 << 20},
		{in: "", wantErr: true},
		{in: "0", wantErr: true},
		{in: "-5", wantErr: true},
		{in: "-5MiB", wantErr: true},
		{in: "MiB", wantErr: true},
		{in: "1.5MiB", wantErr: true},
		{in: "1TiB", wantErr: true},
		{in: "9223372036854775807MiB", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			var b ByteSize
			err := b.Set(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Set(%q) = %d, want an error", tt.in, b)
				}
				return
			}
			if err != nil || int(b) != tt.want {
				t.Fatalf("Set(%q) = %d, %v; want %d", tt.in, b, err, tt.want)
			}
		})
	}
}

func TestByteSizeStringIsReadable(t *testing.T) {
	tests := []struct {
		in   ByteSize
		want string
	}{
		{1 << 20, "1MiB"},
		{256 << 10, "256KiB"},
		{64 << 20, "64MiB"},
		{1 << 30, "1GiB"},
		{1000, "1000"},
		{0, "0"},
	}
	for _, tt := range tests {
		if got := tt.in.String(); got != tt.want {
			t.Errorf("ByteSize(%d).String() = %q, want %q", int(tt.in), got, tt.want)
		}
	}
}

func TestLimitsStringNamesEveryLimit(t *testing.T) {
	got := DefaultLimits().String()
	for _, want := range []string{"wasm 1MiB", "section 256KiB", "sections 10000", "xdr depth 500", "xdr base64 64MiB"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q does not contain %q", got, want)
		}
	}
}
