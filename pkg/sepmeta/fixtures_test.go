package sepmeta

import (
	"io/fs"
	"os"
	"testing"
)

// fixtures is rooted at testdata/wasm; fs.ReadFile rejects names that would
// escape it.
var fixtures = os.DirFS("../../testdata/wasm")

// vectorFS is rooted at testdata/vectors.
var vectorFS = os.DirFS("../../testdata/vectors")

// fixture reads a golden Wasm from testdata/wasm (built by
// scripts/build-fixtures.sh).
func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := fs.ReadFile(fixtures, name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// section returns one custom section of a golden fixture, failing the test
// if the fixture does not have it.
func fixtureSection(t testing.TB, name, section string) []byte {
	t.Helper()
	secs, err := ReadCustomSections(fixture(t, name), []string{section}, DefaultLimits())
	if err != nil {
		t.Fatalf("%s: read sections: %v", name, err)
	}
	s, ok := secs[section]
	if !ok {
		t.Fatalf("%s: no %s section", name, section)
	}
	return s
}
