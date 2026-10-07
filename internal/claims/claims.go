// Package claims finds what a contract says it implements. Every source
// here produces the "declared" tier only: a claim is the contract's own
// statement, never evidence that it is true (CLAUDE.md §5.5, §5.6).
package claims

import (
	"context"
	"errors"
	"fmt"

	"github.com/Soroban-CII/soroindex/pkg/sepmeta"
)

// ErrNotImplemented is returned by sources that exist only as an extension
// point.
var ErrNotImplemented = errors.New("claim source not implemented")

// WasmArtifact is a Wasm module by hash.
type WasmArtifact struct {
	Hash string // lowercase hex
	Code []byte
}

// ContractRef identifies the contract a claim is discovered for. Sources
// that read only the Wasm ignore it.
type ContractRef struct {
	ID string // C... strkey
}

// Claim is one SEP a contract declares, from one source.
type Claim struct {
	SEP      int
	RawToken string
	Source   string // e.g. "sep47-meta"
	Anomaly  string // "" or one of sepmeta's anomaly codes
}

// ClaimSource discovers claims. NeedsInvocation reports whether Discover
// would run or simulate contract code; such sources are skipped unless the
// operator explicitly allows invocation.
type ClaimSource interface {
	Name() string
	NeedsInvocation() bool
	Discover(ctx context.Context, w WasmArtifact, c ContractRef) ([]Claim, error)
}

// SEP47MetaSource reads SEP-47 "sep" meta entries from the Wasm. It parses
// bytes only and never invokes anything.
type SEP47MetaSource struct {
	Limits sepmeta.Limits
	Mode   sepmeta.Mode
}

// SEP47MetaSourceName is the value stored in wasm_claims.source.
const SEP47MetaSourceName = "sep47-meta"

// Name implements ClaimSource.
func (SEP47MetaSource) Name() string { return SEP47MetaSourceName }

// NeedsInvocation implements ClaimSource: reading meta needs no execution.
func (SEP47MetaSource) NeedsInvocation() bool { return false }

// Parse returns the full SEP-47 parse, including tokens that produced no
// SEP, which the store records as parse anomalies. A Wasm without a meta
// section yields empty claims and no error.
func (s SEP47MetaSource) Parse(w WasmArtifact) (sepmeta.SEPClaims, error) {
	secs, err := sepmeta.ReadCustomSections(w.Code, []string{sepmeta.SectionMeta}, s.Limits)
	if err != nil {
		return sepmeta.ParseSEP47(nil, s.Mode), fmt.Errorf("read sections of %s: %w", w.Hash, err)
	}
	meta, ok := secs[sepmeta.SectionMeta]
	if !ok {
		return sepmeta.ParseSEP47(nil, s.Mode), nil
	}
	entries, err := sepmeta.DecodeMeta(meta, s.Limits)
	// Entries decoded before an error are still the contract's statement.
	c := sepmeta.ParseSEP47(entries, s.Mode)
	if err != nil {
		return c, fmt.Errorf("decode meta of %s: %w", w.Hash, err)
	}
	return c, nil
}

// Discover implements ClaimSource. It returns one claim per accepted SEP,
// carrying the first token that produced it. A partial decode returns the
// claims found so far together with the error.
func (s SEP47MetaSource) Discover(_ context.Context, w WasmArtifact, _ ContractRef) ([]Claim, error) {
	c, err := s.Parse(w)
	return FromParse(c, SEP47MetaSourceName), err
}

// FromParse turns a SEP-47 parse into claims: one per accepted SEP, in
// first-seen order, with the raw token and anomaly of its first occurrence.
func FromParse(c sepmeta.SEPClaims, source string) []Claim {
	out := []Claim{}
	seen := map[int]bool{}
	for _, t := range c.Tokens {
		if !t.Accepted || seen[t.SEP] {
			continue
		}
		seen[t.SEP] = true
		out = append(out, Claim{SEP: t.SEP, RawToken: t.Raw, Source: source, Anomaly: t.Anomaly})
	}
	return out
}

// HomeDomainSource would read a claim by calling the contract's
// home_domain() function through a simulateTransaction RPC call. It is a
// stub: it is not registered by default, and Discover always returns
// ErrNotImplemented. It exists so the invocation guard has a real source to
// guard (CLAUDE.md §5.5).
type HomeDomainSource struct{}

// Name implements ClaimSource.
func (HomeDomainSource) Name() string { return "home-domain" }

// NeedsInvocation implements ClaimSource: calling home_domain() would
// simulate contract code.
func (HomeDomainSource) NeedsInvocation() bool { return true }

// Discover implements ClaimSource.
func (HomeDomainSource) Discover(context.Context, WasmArtifact, ContractRef) ([]Claim, error) {
	return nil, ErrNotImplemented
}

// Registry runs a set of sources. Sources that need invocation run only
// when AllowInvocation is true (the --allow-invocation flag); otherwise they
// are skipped and listed by Skipped.
type Registry struct {
	sources         []ClaimSource
	AllowInvocation bool
}

// NewRegistry returns a registry holding sources, in order.
func NewRegistry(sources ...ClaimSource) *Registry {
	return &Registry{sources: sources}
}

// Default is the registry the indexer uses: SEP-47 meta only.
func Default(lim sepmeta.Limits) *Registry {
	return NewRegistry(SEP47MetaSource{Limits: lim, Mode: sepmeta.Lenient})
}

// Active returns the sources that will run.
func (r *Registry) Active() []ClaimSource {
	var out []ClaimSource
	for _, s := range r.sources {
		if !s.NeedsInvocation() || r.AllowInvocation {
			out = append(out, s)
		}
	}
	return out
}

// Skipped returns the names of registered sources that will not run
// because they need invocation and it is not allowed.
func (r *Registry) Skipped() []string {
	var out []string
	for _, s := range r.sources {
		if s.NeedsInvocation() && !r.AllowInvocation {
			out = append(out, s.Name())
		}
	}
	return out
}

// Discover runs every active source and returns all claims. Errors from
// sources are joined; claims found before an error are kept.
func (r *Registry) Discover(ctx context.Context, w WasmArtifact, c ContractRef) ([]Claim, error) {
	var all []Claim
	var errs []error
	for _, s := range r.Active() {
		cl, err := s.Discover(ctx, w, c)
		all = append(all, cl...)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
		}
	}
	return all, errors.Join(errs...)
}
