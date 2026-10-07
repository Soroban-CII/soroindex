package sepmeta

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// leb encodes a non-negative length as minimal unsigned LEB128.
func leb(v int) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

// section encodes one section with the given id and payload.
func section(id byte, payload []byte) []byte {
	out := append([]byte{id}, leb(len(payload))...)
	return append(out, payload...)
}

// custom encodes a custom section (id 0) with a name and body.
func custom(name string, body []byte) []byte {
	payload := append(leb(len(name)), name...)
	return section(0, append(payload, body...))
}

// module concatenates the wasm header with the given raw sections.
func module(sections ...[]byte) []byte {
	out := append([]byte(nil), wasmHeader...)
	for _, s := range sections {
		out = append(out, s...)
	}
	return out
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func TestReadCustomSections(t *testing.T) {
	codeSection := section(10, []byte{0x01, 0x02, 0x03})
	tests := []struct {
		name    string
		wasm    []byte
		names   []string
		want    map[string][]byte
		wantErr error
	}{
		{
			name:    "empty input is not wasm",
			wasm:    nil,
			wantErr: ErrNotWasm,
		},
		{
			name:    "wrong magic is not wasm",
			wasm:    []byte{0x00, 'a', 's', 'n', 0x01, 0, 0, 0},
			wantErr: ErrNotWasm,
		},
		{
			name:    "version 2 is not wasm v1",
			wasm:    []byte{0x00, 'a', 's', 'm', 0x02, 0, 0, 0},
			wantErr: ErrNotWasm,
		},
		{
			name:    "header cut short is not wasm",
			wasm:    wasmHeader[:6],
			wantErr: ErrNotWasm,
		},
		{
			name:  "header only yields no sections",
			wasm:  module(),
			names: []string{"contractmetav0"},
			want:  map[string][]byte{},
		},
		{
			name:  "requested custom section is returned",
			wasm:  module(custom("contractmetav0", []byte("meta"))),
			names: []string{"contractmetav0"},
			want:  map[string][]byte{"contractmetav0": []byte("meta")},
		},
		{
			name:  "unrequested custom section is skipped",
			wasm:  module(custom("producers", []byte("x")), custom("contractspecv0", []byte("spec"))),
			names: []string{"contractspecv0"},
			want:  map[string][]byte{"contractspecv0": []byte("spec")},
		},
		{
			name:  "non-custom sections are skipped",
			wasm:  module(section(1, []byte{0x00}), codeSection, custom("contractmetav0", []byte("m"))),
			names: []string{"contractmetav0"},
			want:  map[string][]byte{"contractmetav0": []byte("m")},
		},
		{
			name: "repeated sections are concatenated in file order",
			wasm: module(
				custom("contractmetav0", []byte("one")),
				codeSection,
				custom("contractspecv0", []byte("s")),
				custom("contractmetav0", []byte("two")),
			),
			names: []string{"contractmetav0", "contractspecv0"},
			want: map[string][]byte{
				"contractmetav0": []byte("onetwo"),
				"contractspecv0": []byte("s"),
			},
		},
		{
			name:  "empty body is present but empty",
			wasm:  module(custom("contractmetav0", nil)),
			names: []string{"contractmetav0"},
			want:  map[string][]byte{"contractmetav0": {}},
		},
		{
			name:  "section named by prefix only does not match",
			wasm:  module(custom("contractmetav0x", []byte("no"))),
			names: []string{"contractmetav0"},
			want:  map[string][]byte{},
		},
		{
			name:  "non-minimal LEB128 size is accepted",
			wasm:  module([]byte{10, 0x82, 0x80, 0x00, 0xaa, 0xbb}, custom("contractmetav0", []byte("m"))),
			names: []string{"contractmetav0"},
			want:  map[string][]byte{"contractmetav0": []byte("m")},
		},
		{
			name:    "section size past end is truncated",
			wasm:    module([]byte{10, 0x05, 0x01}),
			wantErr: ErrTruncated,
		},
		{
			name:    "section id with no size is truncated",
			wasm:    module([]byte{10}),
			wantErr: ErrTruncated,
		},
		{
			name:    "LEB128 cut mid-value is truncated",
			wasm:    module([]byte{10, 0x80}),
			wantErr: ErrTruncated,
		},
		{
			name:    "LEB128 with a fifth byte over 32 bits is malformed",
			wasm:    module([]byte{10, 0xff, 0xff, 0xff, 0xff, 0x1f}),
			wantErr: ErrMalformed,
		},
		{
			name:    "LEB128 longer than five bytes is malformed",
			wasm:    module([]byte{10, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00}),
			wantErr: ErrMalformed,
		},
		{
			name:    "max u32 section size on short input is truncated, not allocated",
			wasm:    module([]byte{0, 0xff, 0xff, 0xff, 0xff, 0x0f}),
			wantErr: ErrTruncated,
		},
		{
			name:    "custom name longer than its section is truncated",
			wasm:    module(section(0, []byte{0x09, 'a', 'b'})),
			wantErr: ErrTruncated,
		},
		{
			name:    "custom section with no name length is truncated",
			wasm:    module(section(0, nil)),
			wantErr: ErrTruncated,
		},
		{
			name:    "non-UTF-8 custom name is malformed",
			wasm:    module(section(0, []byte{0x02, 0xff, 0xfe})),
			wantErr: ErrMalformed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadCustomSections(tt.wasm, tt.names, DefaultLimits())
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if got != nil {
					t.Fatalf("got %v alongside an error, want nil", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d sections %v, want %d %v", len(got), got, len(tt.want), tt.want)
			}
			for k, v := range tt.want {
				g, ok := got[k]
				if !ok || !bytes.Equal(g, v) {
					t.Errorf("section %q = %q (present %v), want %q", k, g, ok, v)
				}
			}
		})
	}
}

func TestReadCustomSectionsLimits(t *testing.T) {
	small := Limits{MaxWasmBytes: 64, MaxSectionBytes: 8, MaxSections: 3}
	tests := []struct {
		name      string
		wasm      []byte
		lim       Limits
		wantLimit string
	}{
		{
			name:      "module over MaxWasmBytes is rejected before parsing",
			wasm:      module(custom("x", make([]byte, 64))),
			lim:       small,
			wantLimit: "MaxWasmBytes",
		},
		{
			name:      "one requested section over MaxSectionBytes is rejected",
			wasm:      module(custom("m", make([]byte, 9))),
			lim:       small,
			wantLimit: "MaxSectionBytes",
		},
		{
			name:      "repeated sections are limited by their concatenated size",
			wasm:      module(custom("m", make([]byte, 5)), custom("m", make([]byte, 5))),
			lim:       small,
			wantLimit: "MaxSectionBytes",
		},
		{
			name:      "section count over MaxSections is rejected",
			wasm:      module(section(1, nil), section(1, nil), section(1, nil), section(1, nil)),
			lim:       small,
			wantLimit: "MaxSections",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadCustomSections(tt.wasm, []string{"m"}, tt.lim)
			if !errors.Is(err, ErrLimit) {
				t.Fatalf("err = %v, want ErrLimit", err)
			}
			var le *LimitError
			if !errors.As(err, &le) || le.Limit != tt.wantLimit {
				t.Fatalf("limit = %+v, want %s", le, tt.wantLimit)
			}
			if !strings.Contains(err.Error(), tt.wantLimit) {
				t.Errorf("message %q does not name the limit", err.Error())
			}
		})
	}

	t.Run("unrequested sections do not count against MaxSectionBytes", func(t *testing.T) {
		w := module(custom("big", make([]byte, 40)), custom("m", []byte("ok")))
		got, err := ReadCustomSections(w, []string{"m"}, small)
		if err != nil || string(got["m"]) != "ok" {
			t.Fatalf("got %q, %v; want \"ok\", nil", got["m"], err)
		}
	})
	t.Run("exactly MaxSections sections is allowed", func(t *testing.T) {
		w := module(section(1, nil), section(1, nil), section(1, nil))
		if _, err := ReadCustomSections(w, nil, small); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
	})
}

func TestReadCustomSectionsDoesNotAliasInput(t *testing.T) {
	w := module(custom("m", []byte("abc")))
	got, err := ReadCustomSections(w, []string{"m"}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	for i := range w {
		w[i] = 0
	}
	if string(got["m"]) != "abc" {
		t.Fatalf("result changed with input: %q", got["m"])
	}
}

func TestLEB128u32(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want uint32
	}{
		{"zero", []byte{0x00}, 0},
		{"one byte max", []byte{0x7f}, 127},
		{"two bytes", []byte{0x80, 0x01}, 128},
		{"u32 max in five bytes", []byte{0xff, 0xff, 0xff, 0xff, 0x0f}, 0xffffffff},
		{"non-minimal zero", []byte{0x80, 0x80, 0x00}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := reader{buf: tt.in}
			got, err := r.u32()
			if err != nil || got != tt.want {
				t.Fatalf("u32(%x) = %d, %v; want %d", tt.in, got, err, tt.want)
			}
			if r.remaining() != 0 {
				t.Fatalf("left %d bytes unread", r.remaining())
			}
		})
	}
}
