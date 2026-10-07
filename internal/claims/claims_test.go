package claims

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"reflect"
	"testing"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

func fixture(t *testing.T, name string) WasmArtifact {
	t.Helper()
	b, err := fs.ReadFile(os.DirFS("../../testdata/wasm"), name)
	if err != nil {
		t.Fatal(err)
	}
	return WasmArtifact{Hash: name, Code: b}
}

func TestSEP47MetaSourceFixtures(t *testing.T) {
	src := SEP47MetaSource{Limits: sepmeta.DefaultLimits()}
	tests := []struct {
		fixture string
		want    []Claim
		wantErr error
	}{
		{"token_full_sep.wasm", []Claim{{SEP: 41, RawToken: "41", Source: "sep47-meta"}}, nil},
		{"multi_sep.wasm", []Claim{{SEP: 41, RawToken: "41", Source: "sep47-meta"}, {SEP: 40, RawToken: "40", Source: "sep47-meta"}}, nil},
		{"token_full_legacy.wasm", []Claim{}, nil},
		{"no_meta.wasm", []Claim{}, nil},
		{"truncated.wasm", []Claim{}, sepmeta.ErrTruncated},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			got, err := src.Discover(context.Background(), fixture(t, tt.fixture), ContractRef{})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("claims = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFromParseKeepsAnomalyAndFirstToken(t *testing.T) {
	c := sepmeta.ParseSEP47([]sepmeta.MetaEntry{{Key: "sep", Value: "041,41,abc"}}, sepmeta.Lenient)
	got := FromParse(c, "sep47-meta")
	want := []Claim{{SEP: 41, RawToken: "041", Source: "sep47-meta", Anomaly: sepmeta.AnomalyLeadingZero}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if strict := FromParse(sepmeta.ParseSEP47([]sepmeta.MetaEntry{{Key: "sep", Value: "041"}}, sepmeta.Strict), "x"); len(strict) != 0 {
		t.Fatalf("strict mode produced claims from a rejected token: %+v", strict)
	}
}

func TestHomeDomainSourceIsAStub(t *testing.T) {
	var s HomeDomainSource
	if !s.NeedsInvocation() {
		t.Fatal("HomeDomainSource must report that it needs invocation")
	}
	if _, err := s.Discover(context.Background(), WasmArtifact{}, ContractRef{}); !errors.Is(err, ErrNotImplemented) {
		t.Fatalf("err = %v, want ErrNotImplemented", err)
	}
}

// countingSource records whether it ran.
type countingSource struct {
	name  string
	needs bool
	calls int
}

func (c *countingSource) Name() string          { return c.name }
func (c *countingSource) NeedsInvocation() bool { return c.needs }
func (c *countingSource) Discover(context.Context, WasmArtifact, ContractRef) ([]Claim, error) {
	c.calls++
	return []Claim{{SEP: 1, Source: c.name}}, nil
}

func TestInvocationGuard(t *testing.T) {
	t.Run("a registered invocation-needing source is skipped without --allow-invocation", func(t *testing.T) {
		meta := &countingSource{name: "meta"}
		invoking := &countingSource{name: "invoking", needs: true}
		r := NewRegistry(meta, invoking)
		claims, err := r.Discover(context.Background(), WasmArtifact{}, ContractRef{})
		if err != nil {
			t.Fatal(err)
		}
		if invoking.calls != 0 || meta.calls != 1 || len(claims) != 1 {
			t.Fatalf("invoking ran %d times, meta %d, claims %d", invoking.calls, meta.calls, len(claims))
		}
		if !reflect.DeepEqual(r.Skipped(), []string{"invoking"}) {
			t.Fatalf("skipped = %v", r.Skipped())
		}
	})
	t.Run("it runs only when invocation is allowed", func(t *testing.T) {
		invoking := &countingSource{name: "invoking", needs: true}
		r := NewRegistry(invoking)
		r.AllowInvocation = true
		if _, err := r.Discover(context.Background(), WasmArtifact{}, ContractRef{}); err != nil || invoking.calls != 1 {
			t.Fatalf("calls = %d, err %v", invoking.calls, err)
		}
		if len(r.Skipped()) != 0 {
			t.Fatalf("skipped = %v", r.Skipped())
		}
	})
	t.Run("the HomeDomainSource stub is skipped by the same guard", func(t *testing.T) {
		r := NewRegistry(HomeDomainSource{})
		if len(r.Active()) != 0 || !reflect.DeepEqual(r.Skipped(), []string{"home-domain"}) {
			t.Fatalf("active %v skipped %v", r.Active(), r.Skipped())
		}
	})
	t.Run("the default registry holds no invocation-needing source", func(t *testing.T) {
		r := Default(sepmeta.DefaultLimits())
		r.AllowInvocation = true // even when allowed, nothing shipped invokes
		for _, s := range r.Active() {
			if s.NeedsInvocation() {
				t.Fatalf("default registry includes %s, which needs invocation", s.Name())
			}
		}
		if len(r.Active()) != 1 || r.Active()[0].Name() != SEP47MetaSourceName {
			t.Fatalf("default sources = %v", r.Active())
		}
	})
}
