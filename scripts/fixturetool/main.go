// Command fixturetool derives golden Wasm fixtures that a compiler cannot
// produce directly: one with a custom section removed, and one cut short in
// the middle of a section.
//
// It walks sections with its own minimal reader instead of pkg/sepmeta, so
// the fixtures do not depend on the code they are used to test.
//
// Usage:
//
//	fixturetool strip    -section NAME IN OUT   # drop every custom section NAME
//	fixturetool truncate -section NAME IN OUT   # cut IN halfway through section NAME
//	fixturetool metawasm VECTOR.txt OUT         # module whose only section is
//	                                            # contractmetav0 holding one
//	                                            # "sep" entry per vector line
//
// metawasm wraps a meta vector (testdata/vectors/*.txt) in a minimal module
// so tools that only read Wasm, such as stellar-cli and the Python SDK, can
// be run on it by scripts/record-conformance.sh. It encodes the XDR by hand;
// pkg/sepmeta's conformance test builds the same bytes and checks the hash.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

var header = []byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00}

// span locates one section within a module.
type span struct {
	start, payload, end int // section id offset, payload offset, end offset
	custom              string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fixturetool:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: fixturetool strip|truncate -section NAME IN OUT")
	}
	if args[0] == "metawasm" {
		if len(args) != 3 {
			return errors.New("usage: fixturetool metawasm VECTOR.txt OUT")
		}
		vec, err := os.ReadFile(args[1]) // #nosec G304 G703 -- developer-supplied vector path
		if err != nil {
			return err
		}
		mod, err := metaWasm(vec)
		if err != nil {
			return fmt.Errorf("%s: %w", args[1], err)
		}
		return os.WriteFile(args[2], mod, 0o644) // #nosec G306 G703 -- developer-supplied path; public test data
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	name := fs.String("section", "", "custom section name")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *name == "" || fs.NArg() != 2 {
		return errors.New("need -section NAME and IN OUT")
	}
	in, out := fs.Arg(0), fs.Arg(1)
	wasm, err := os.ReadFile(in) // #nosec G304 G703 -- path is a developer-supplied fixture path, not network input
	if err != nil {
		return err
	}
	spans, err := sections(wasm)
	if err != nil {
		return fmt.Errorf("%s: %w", in, err)
	}
	var result []byte
	switch args[0] {
	case "strip":
		result, err = strip(wasm, spans, *name)
	case "truncate":
		result, err = truncate(wasm, spans, *name)
	default:
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
	if err != nil {
		return fmt.Errorf("%s: %w", in, err)
	}
	return os.WriteFile(out, result, 0o644) // #nosec G306 G703 -- path is developer-supplied; fixtures are public test data
}

func strip(wasm []byte, spans []span, name string) ([]byte, error) {
	out := append([]byte(nil), wasm[:len(header)]...)
	removed := 0
	for _, s := range spans {
		if s.custom == name {
			removed++
			continue
		}
		out = append(out, wasm[s.start:s.end]...)
	}
	if removed == 0 {
		return nil, fmt.Errorf("no custom section %q", name)
	}
	return out, nil
}

func truncate(wasm []byte, spans []span, name string) ([]byte, error) {
	for _, s := range spans {
		if s.custom == name {
			return append([]byte(nil), wasm[:s.payload+(s.end-s.payload)/2]...), nil
		}
	}
	return nil, fmt.Errorf("no custom section %q", name)
}

func sections(wasm []byte) ([]span, error) {
	if !bytes.HasPrefix(wasm, header) {
		return nil, errors.New("not a wasm v1 module")
	}
	var spans []span
	off := len(header)
	for off < len(wasm) {
		start := off
		id := wasm[off]
		off++
		size, n, err := uleb(wasm[off:])
		if err != nil {
			return nil, err
		}
		off += n
		if size > len(wasm)-off {
			return nil, fmt.Errorf("section at %d overruns input", start)
		}
		s := span{start: start, payload: off, end: off + size}
		if id == 0 {
			nlen, m, err := uleb(wasm[off:s.end])
			if err != nil || nlen > s.end-off-m {
				return nil, fmt.Errorf("bad custom section name at %d", start)
			}
			s.custom = string(wasm[off+m : off+m+nlen])
		}
		spans = append(spans, s)
		off = s.end
	}
	return spans, nil
}

func uleb(b []byte) (v, n int, err error) {
	for i := 0; i < 5 && i < len(b); i++ {
		v |= int(b[i]&0x7f) << (7 * i)
		if b[i]&0x80 == 0 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errors.New("bad LEB128")
}

// metaWasm builds a module with one contractmetav0 custom section. Each
// "\n"-terminated line of vec becomes an SCMetaEntry{SC_META_V0, "sep",
// line}, the same reading of a vector as pkg/sepmeta's TestVectors.
func metaWasm(vec []byte) ([]byte, error) {
	s := string(vec)
	if !strings.HasSuffix(s, "\n") {
		return nil, errors.New("last line must end in \\n")
	}
	var meta []byte
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		meta = append(meta, 0, 0, 0, 0) // SC_META_V0
		meta = xdrString(meta, "sep")
		meta = xdrString(meta, line)
	}
	const name = "contractmetav0"
	payload := append(leb(len(name)), name...)
	payload = append(payload, meta...)
	out := append([]byte(nil), header...)
	out = append(out, 0)
	out = append(out, leb(len(payload))...)
	return append(out, payload...), nil
}

// xdrString appends an XDR string: a big-endian u32 length, the bytes, and
// zero padding to a multiple of four.
func xdrString(b []byte, s string) []byte {
	n := uint32(len(s)) // #nosec G115 -- vector lines are far below 4 GiB
	b = binary.BigEndian.AppendUint32(b, n)
	b = append(b, s...)
	return append(b, make([]byte, (4-len(s)%4)%4)...)
}

func leb(v int) []byte {
	var out []byte
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v == 0 {
			return append(out, c)
		}
		out = append(out, c|0x80)
	}
}
