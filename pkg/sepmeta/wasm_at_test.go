package sepmeta

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// sentinelOf names the package sentinel err matches, or "" for nil and
// "unclassified" for anything else.
func sentinelOf(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrNotWasm):
		return "ErrNotWasm"
	case errors.Is(err, ErrTruncated):
		return "ErrTruncated"
	case errors.Is(err, ErrMalformed):
		return "ErrMalformed"
	case errors.Is(err, ErrLimit):
		return "ErrLimit"
	}
	return "unclassified"
}

// compareReaders runs both implementations on the same bytes and reports the
// first way they differ: in the error message, the sentinel, the set of
// sections returned, or any section's bytes. "" means identical.
func compareReaders(wasm []byte, names []string, lim Limits) string {
	want, wantErr := ReadCustomSections(wasm, names, lim)
	got, gotErr := ReadCustomSectionsAt(bytes.NewReader(wasm), int64(len(wasm)), names, lim)
	if sentinelOf(gotErr) != sentinelOf(wantErr) {
		return fmt.Sprintf("error class %q (%v), reference %q (%v)", sentinelOf(gotErr), gotErr, sentinelOf(wantErr), wantErr)
	}
	if fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		return fmt.Sprintf("error %q, reference %q", gotErr, wantErr)
	}
	var gle, wle *LimitError
	if errors.As(gotErr, &gle) != errors.As(wantErr, &wle) || (gle != nil && *gle != *wle) {
		return fmt.Sprintf("limit error %+v, reference %+v", gle, wle)
	}
	if (got == nil) != (want == nil) || len(got) != len(want) {
		return fmt.Sprintf("sections %q, reference %q", keys(got), keys(want))
	}
	for k, w := range want {
		g, ok := got[k]
		if !ok {
			return fmt.Sprintf("section %q missing", k)
		}
		if !bytes.Equal(g, w) {
			return fmt.Sprintf("section %q: %d bytes differ from reference %d bytes", k, len(g), len(w))
		}
	}
	return ""
}

func keys(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

// nameSets are the name lists the differential tests request. They cover
// nothing, each real section, both, a name no fixture has, and a name long
// enough that unrequested names are compared through the chunked path.
var nameSets = [][]string{
	nil,
	{SectionMeta},
	{SectionSpec},
	{SectionMeta, SectionSpec},
	{SectionMeta, SectionSpec, "contractenvmetav0", "absent"},
	{strings.Repeat("n", nameChunk+10)},
}

func TestReadCustomSectionsAtMatchesReferenceOnFixtures(t *testing.T) {
	names, err := fs.Glob(fixtures, "*.wasm")
	if err != nil || len(names) == 0 {
		t.Fatalf("no fixtures: %v", err)
	}
	small := Limits{MaxWasmBytes: 4096, MaxSectionBytes: 512, MaxSections: 16}
	for _, n := range names {
		w := fixture(t, n)
		for _, lim := range []Limits{DefaultLimits(), small, {MaxSections: 3}} {
			for _, ns := range nameSets {
				t.Run(fmt.Sprintf("%s/%v/%d names agree with ReadCustomSections", n, lim, len(ns)), func(t *testing.T) {
					if d := compareReaders(w, ns, lim); d != "" {
						t.Fatal(d)
					}
				})
			}
		}
	}
}

// TestReadCustomSectionsAtMatchesReferenceOnCorpus runs the differential over
// every committed fuzz corpus entry, the generated corpus included when the
// cache directory is given in SEPMETA_FUZZ_CORPUS (a directory of corpus
// files in the "go test fuzz v1" format). The fuzz target checks the same
// thing for every input it generates.
func TestReadCustomSectionsAtMatchesReferenceOnCorpus(t *testing.T) {
	dirs := []string{filepath.Join("testdata", "fuzz", "FuzzReadCustomSections")}
	if d := os.Getenv("SEPMETA_FUZZ_CORPUS"); d != "" {
		dirs = append(dirs, d)
	}
	total := 0
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			b, err := os.ReadFile(filepath.Join(dir, e.Name())) // #nosec G304 G703 -- corpus directory chosen by the developer
			if err != nil {
				t.Fatal(err)
			}
			wasm, err := corpusBytes(b)
			if err != nil {
				t.Fatalf("%s: %v", e.Name(), err)
			}
			total++
			for _, lim := range []Limits{DefaultLimits(), {MaxWasmBytes: 4096, MaxSectionBytes: 512, MaxSections: 16}} {
				if d := compareReaders(wasm, []string{SectionMeta, SectionSpec}, lim); d != "" {
					t.Errorf("%s: %s", e.Name(), d)
				}
			}
		}
	}
	t.Logf("compared %d corpus entries", total)
}

// corpusBytes decodes a one-argument []byte corpus file.
func corpusBytes(b []byte) ([]byte, error) {
	lines := strings.SplitN(string(b), "\n", 3)
	if len(lines) < 2 || lines[0] != "go test fuzz v1" {
		return nil, errors.New("not a go test fuzz v1 file")
	}
	v := strings.TrimSpace(lines[1])
	if !strings.HasPrefix(v, "[]byte(") || !strings.HasSuffix(v, ")") {
		return nil, fmt.Errorf("not a []byte value: %.40s", v)
	}
	var s string
	if _, err := fmt.Sscanf(v[len("[]byte("):len(v)-1], "%q", &s); err != nil {
		return nil, err
	}
	return []byte(s), nil
}

func TestReadCustomSectionsAtMatchesReferenceOnCraftedModules(t *testing.T) {
	long := strings.Repeat("a", nameChunk-1) + "€" + "b" // "€" (3 bytes) straddles the first chunk boundary
	badSplit := []byte(strings.Repeat("a", nameChunk-1))
	badSplit = append(badSplit, 0xe2, 0x28, 0xa1) // invalid sequence across the boundary
	cutRune := []byte(strings.Repeat("a", nameChunk+5))
	cutRune = append(cutRune, 0xe2, 0x82) // name ends inside a 3-byte character
	tests := []struct {
		name string
		wasm []byte
	}{
		{"empty input", nil},
		{"seven bytes", wasmHeader[:7]},
		{"header only", module()},
		{"wrong version", []byte{0x00, 'a', 's', 'm', 0x02, 0, 0, 0}},
		{"code and custom", module(section(10, []byte{1, 2, 3}), custom(SectionMeta, []byte("m")))},
		{"repeated meta concatenates", module(custom(SectionMeta, []byte("ab")), section(1, nil), custom(SectionMeta, []byte("cd")))},
		{"empty requested body is present", module(custom(SectionMeta, nil))},
		{"section size past end", cat(module(), []byte{1, 0x05, 0x00})},
		{"truncated section size", cat(module(), []byte{1, 0x80})},
		{"six-byte LEB128", cat(module(), []byte{1, 0x80, 0x80, 0x80, 0x80, 0x80, 0x00})},
		{"fifth LEB128 byte over 32 bits", cat(module(), []byte{1, 0x80, 0x80, 0x80, 0x80, 0x10})},
		{"custom section with no name length", module(section(0, nil))},
		{"custom name length past payload", module(section(0, []byte{0x05, 'a'}))},
		{"custom name LEB128 truncated by payload", module(section(0, []byte{0x80}))},
		{"non-UTF-8 short name", module(section(0, []byte{0x02, 0xff, 0xfe}))},
		{"non-UTF-8 name of requested length", module(section(0, cat(leb(len(SectionMeta)), bytes.Repeat([]byte{0xff}, len(SectionMeta)))))},
		{"long valid name split across chunks", module(custom(long, []byte("x")), custom(SectionSpec, []byte("s")))},
		{"long invalid name split across chunks", module(section(0, cat(leb(len(badSplit)), badSplit)))},
		{"long name ending mid-character", module(section(0, cat(leb(len(cutRune)), cutRune)))},
		{"name exactly one chunk", module(custom(strings.Repeat("z", nameChunk), nil), custom(SectionMeta, []byte("m")))},
	}
	lims := []Limits{DefaultLimits(), {MaxWasmBytes: 64, MaxSectionBytes: 2, MaxSections: 2}}
	for _, tt := range tests {
		for _, lim := range lims {
			for _, ns := range nameSets {
				t.Run(fmt.Sprintf("%s/%d/%d names agree with ReadCustomSections", tt.name, lim.MaxSections, len(ns)), func(t *testing.T) {
					if d := compareReaders(tt.wasm, ns, lim); d != "" {
						t.Fatal(d)
					}
				})
			}
		}
	}
}

func TestReadCustomSectionsAtLimits(t *testing.T) {
	small := Limits{MaxWasmBytes: 64, MaxSectionBytes: 8, MaxSections: 3}
	tests := []struct {
		name      string
		wasm      []byte
		wantLimit string
	}{
		{"module over MaxWasmBytes is rejected before any read", module(custom("x", make([]byte, 64))), "MaxWasmBytes"},
		{"requested section over MaxSectionBytes is rejected", module(custom("m", make([]byte, 9))), "MaxSectionBytes"},
		{"repeated sections are limited by their concatenated size", module(custom("m", make([]byte, 5)), custom("m", make([]byte, 5))), "MaxSectionBytes"},
		{"section count over MaxSections is rejected", module(section(1, nil), section(1, nil), section(1, nil), section(1, nil)), "MaxSections"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ReadCustomSectionsAt(bytes.NewReader(tt.wasm), int64(len(tt.wasm)), []string{"m"}, small)
			var le *LimitError
			if !errors.Is(err, ErrLimit) || !errors.As(err, &le) || le.Limit != tt.wantLimit {
				t.Fatalf("err = %v, want ErrLimit for %s", err, tt.wantLimit)
			}
		})
	}
	t.Run("a declared size over MaxWasmBytes is rejected without reading", func(t *testing.T) {
		r := &countingReaderAt{r: bytes.NewReader(module())}
		_, err := ReadCustomSectionsAt(r, 1<<40, nil, DefaultLimits())
		var le *LimitError
		if !errors.As(err, &le) || le.Limit != "MaxWasmBytes" || r.calls != 0 {
			t.Fatalf("err = %v after %d reads, want MaxWasmBytes and no reads", err, r.calls)
		}
	})
	t.Run("a negative size is malformed", func(t *testing.T) {
		if _, err := ReadCustomSectionsAt(bytes.NewReader(nil), -1, nil, DefaultLimits()); !errors.Is(err, ErrMalformed) {
			t.Fatalf("err = %v, want ErrMalformed", err)
		}
	})
}

// countingReaderAt records how many reads and bytes were requested.
type countingReaderAt struct {
	r     io.ReaderAt
	calls int
	bytes int
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.calls++
	c.bytes += len(p)
	return c.r.ReadAt(p, off)
}

// shortReaderAt returns at most limit bytes of r, then reports err.
type shortReaderAt struct {
	r     []byte
	limit int
	err   error
}

func (s shortReaderAt) ReadAt(p []byte, off int64) (int, error) {
	end := min(int64(s.limit), int64(len(s.r)))
	if off >= end {
		return 0, s.err
	}
	n := copy(p, s.r[off:end])
	if n < len(p) {
		return n, s.err
	}
	return n, nil
}

func TestReadCustomSectionsAtShortRead(t *testing.T) {
	w := fixture(t, "token_full_sep.wasm")
	names := []string{SectionMeta, SectionSpec}
	ioErr := errors.New("disk on fire")
	for _, cut := range []int{0, 4, 8, 9, len(w) / 2, len(w) - 1} {
		for _, readErr := range []error{io.EOF, io.ErrUnexpectedEOF, nil} {
			t.Run(fmt.Sprintf("cut at %d with %v is ErrTruncated or unaffected", cut, readErr), func(t *testing.T) {
				got, err := ReadCustomSectionsAt(shortReaderAt{r: w, limit: cut, err: readErr}, int64(len(w)), names, DefaultLimits())
				if err == nil {
					// The missing tail lay inside a section that was never
					// read, so the result must equal the full read.
					if d := compareReaders(w, names, DefaultLimits()); d != "" || len(got) != 2 {
						t.Fatalf("no error but result differs: %s", d)
					}
					return
				}
				if !errors.Is(err, ErrTruncated) || got != nil {
					t.Fatalf("err = %v (result %v), want ErrTruncated and nil", err, got != nil)
				}
			})
		}
		t.Run(fmt.Sprintf("cut at %d with an I/O error passes the error through", cut), func(t *testing.T) {
			_, err := ReadCustomSectionsAt(shortReaderAt{r: w, limit: cut, err: ioErr}, int64(len(w)), names, DefaultLimits())
			if err == nil {
				return // the cut fell inside a skipped section
			}
			if !errors.Is(err, ioErr) || isClassified(err) {
				t.Fatalf("err = %v, want the I/O error and no sentinel", err)
			}
		})
	}
	t.Run("a cut inside a requested section is always ErrTruncated", func(t *testing.T) {
		body := bytes.Repeat([]byte("m"), 100)
		m := module(section(10, make([]byte, 50)), custom(SectionMeta, body))
		for cut := len(m) - len(body); cut < len(m); cut += 7 {
			_, err := ReadCustomSectionsAt(shortReaderAt{r: m, limit: cut, err: io.EOF}, int64(len(m)), names, DefaultLimits())
			if !errors.Is(err, ErrTruncated) {
				t.Fatalf("cut at %d: err = %v, want ErrTruncated", cut, err)
			}
		}
	})
}

func TestReadCustomSectionsAtSkipsUnrequestedBytes(t *testing.T) {
	big := make([]byte, 200_000)
	w := module(section(10, big), custom("other", big), custom(SectionMeta, []byte("m")))
	r := &countingReaderAt{r: bytes.NewReader(w)}
	got, err := ReadCustomSectionsAt(r, int64(len(w)), []string{SectionMeta}, DefaultLimits())
	if err != nil || string(got[SectionMeta]) != "m" {
		t.Fatalf("got %q, %v", got[SectionMeta], err)
	}
	// Header, three section heads, two name lengths, two names and one
	// one-byte body: well under 100 bytes of a 400 kB module.
	if r.bytes > 100 {
		t.Fatalf("read %d bytes of a %d-byte module, want under 100", r.bytes, len(w))
	}
	t.Logf("read %d bytes in %d calls from a %d-byte module", r.bytes, r.calls, len(w))
}

func TestCompleteRunes(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"ab\xe2\x82", 2},     // incomplete € is held back
		{"ab\xe2\x82\xac", 5}, // complete €
		{"ab\xf0\x9f\x98", 2}, // incomplete 4-byte character
		{"ab\xff", 3},         // never valid: left for utf8.Valid to reject
		{"ab\x82", 3},         // stray continuation byte
	}
	for _, tt := range tests {
		if got := completeRunes([]byte(tt.in)); got != tt.want {
			t.Errorf("completeRunes(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
	if !utf8.Valid([]byte("ab\xe2\x82\xac")) {
		t.Fatal("test data")
	}
}

// oneMiBModule returns a module of exactly 1 MiB: token_full_sep's sections,
// a code section padding it out, and a long-named unrequested custom section.
// It also returns how many payload bytes a read of meta and spec returns.
func oneMiBModule(t testing.TB) ([]byte, int) {
	t.Helper()
	src := fixture(t, "token_full_sep.wasm")
	secs, err := ReadCustomSections(src, []string{SectionMeta, SectionSpec}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	payload := len(secs[SectionMeta]) + len(secs[SectionSpec])
	base := module(
		custom(SectionMeta, secs[SectionMeta]),
		custom(strings.Repeat("x", 3*nameChunk), make([]byte, 1000)),
		custom(SectionSpec, secs[SectionSpec]),
	)
	const target = 1 << 20
	// A code section of n payload bytes costs 1 id byte, its LEB128 size
	// (3 bytes for any n in [2^14, 2^21)) and n.
	pad := target - len(base) - 1 - 3
	w := cat(base, section(10, make([]byte, pad)))
	if len(w) != target {
		t.Fatalf("module is %d bytes, want %d", len(w), target)
	}
	return w, payload
}

// memoryBound is the stated bound on bytes allocated by one
// ReadCustomSectionsAt call: the payloads it returns plus 16 KiB of fixed
// overhead (the result map, name buffers and the name-validation chunk).
func memoryBound(payload int) int64 { return int64(payload) + 16<<10 }

func TestReadCustomSectionsAtAllocationBound(t *testing.T) {
	if testing.Short() {
		t.Skip("measures allocation with testing.Benchmark")
	}
	w, payload := oneMiBModule(t)
	r := bytes.NewReader(w)
	names := []string{SectionMeta, SectionSpec}
	res := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := ReadCustomSectionsAt(r, int64(len(w)), names, DefaultLimits()); err != nil {
				b.Fatal(err)
			}
		}
	})
	got := res.AllocedBytesPerOp()
	if got > memoryBound(payload) {
		t.Fatalf("allocated %d B/op on a 1 MiB module, bound is %d (payload %d + 16 KiB)", got, memoryBound(payload), payload)
	}
	t.Logf("1 MiB module: %d B/op, %d allocs/op; payload %d B; bound %d B", got, res.AllocsPerOp(), payload, memoryBound(payload))
}

// BenchmarkReadCustomSections1MiB compares reading meta and spec from a
// 1 MiB module on disk two ways: loading the whole file and calling
// ReadCustomSections, and calling ReadCustomSectionsAt on the open file.
// B/op is what each needs resident.
func BenchmarkReadCustomSections1MiB(b *testing.B) {
	w, payload := oneMiBModule(b)
	path := filepath.Join(b.TempDir(), "big.wasm")
	if err := os.WriteFile(path, w, 0o600); err != nil {
		b.Fatal(err)
	}
	f, err := os.Open(path) // #nosec G304 -- temp file created above
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = f.Close() }() // read-only file; a close error loses nothing
	names := []string{SectionMeta, SectionSpec}
	b.Logf("module %d B, requested payload %d B, stated bound %d B", len(w), payload, memoryBound(payload))
	b.Run("ReadFile+ReadCustomSections", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			all, err := os.ReadFile(path) // #nosec G304 -- temp file created above
			if err != nil {
				b.Fatal(err)
			}
			if _, err := ReadCustomSections(all, names, DefaultLimits()); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("ReadCustomSectionsAt", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := ReadCustomSectionsAt(f, int64(len(w)), names, DefaultLimits()); err != nil {
				b.Fatal(err)
			}
		}
	})
}
