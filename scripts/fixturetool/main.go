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
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
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
