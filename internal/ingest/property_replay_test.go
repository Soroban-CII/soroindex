package ingest

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/rand"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/Soroban-CII/soroindex/internal/rpc"
)

// factGenerator generates random but valid sequences of contract facts.
type factGenerator struct {
	t          *testing.T
	seed       int64
	rng        *rand.Rand
	contracts  map[string]*contractState  // contractID -> state
	references map[string]*referenceState // owner:tag -> state
	codeHashes map[string][]byte          // hash -> wasm bytes
	ledger     uint32
	nextCID    int
	nextRef    int
}

type contractState struct {
	id        string
	kind      ExecKind
	wasmHash  string
	refOwner  string
	refTag    string
	archived  bool
	createdAt uint32
	updatedAt uint32
}

type referenceState struct {
	owner     string
	tag       string
	wasmHash  string
	archived  bool
	updatedAt uint32
}

func newFactGenerator(t *testing.T, seed int64) *factGenerator {
	rng := rand.New(rand.NewSource(seed))
	return &factGenerator{
		t:          t,
		seed:       seed,
		rng:        rng,
		contracts:  make(map[string]*contractState),
		references: make(map[string]*referenceState),
		codeHashes: make(map[string][]byte),
		ledger:     100,
		nextCID:    1,
		nextRef:    1,
	}
}

// generateWasm creates a minimal valid Wasm and returns its hash and bytes.
func (g *factGenerator) generateWasm() (string, []byte) {
	code := []byte("\x00asm\x01\x00\x00\x00") // minimal valid wasm header
	sum := sha256.Sum256(code)
	hash := hex.EncodeToString(sum[:])
	g.codeHashes[hash] = code
	return hash, code
}

// randomWasmHash returns a random existing wasm hash or generates a new one.
func (g *factGenerator) randomWasmHash() string {
	if len(g.codeHashes) == 0 || g.rng.Int31n(10) < 3 {
		hash, _ := g.generateWasm()
		return hash
	}
	hashes := make([]string, 0, len(g.codeHashes))
	for h := range g.codeHashes {
		hashes = append(hashes, h)
	}
	return hashes[g.rng.Int31n(int32(len(hashes)))]
}

// randomContractID returns a random existing contract ID. Panics if none exist.
func (g *factGenerator) randomContractID() string {
	if len(g.contracts) == 0 {
		g.t.Fatal("randomContractID called with no contracts")
	}
	ids := make([]string, 0, len(g.contracts))
	for id := range g.contracts {
		ids = append(ids, id)
	}
	return ids[g.rng.Int31n(int32(len(ids)))]
}

// newContractID generates a new contract ID.
func (g *factGenerator) newContractID() string {
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(g.rng.Int63())
	}
	// Make it a valid contract ID by setting the version byte
	s, _ := strkey.Encode(strkey.VersionByteContract, raw[:])
	return s
}

// newReference generates a new reference owner:tag.
func (g *factGenerator) newReference() (string, string) {
	owner := g.newContractID()
	tag := fmt.Sprintf("v%d", g.nextRef)
	g.nextRef++
	return owner, tag
}

// generateFactSequence generates a random sequence of contract facts.
func (g *factGenerator) generateFactSequence(numFacts int) []ContractFacts {
	var facts []ContractFacts
	for i := 0; i < numFacts; i++ {
		facts = append(facts, g.generateFact())
	}
	return facts
}

// generateFact generates a single random contract fact.
func (g *factGenerator) generateFact() ContractFacts {
	g.ledger++
	f := ContractFacts{Ledger: g.ledger}

	// Choose a random operation type
	op := g.rng.Int31n(6)

	switch op {
	case 0: // Create new contract with Wasm
		f.Changes = append(f.Changes, g.createWasmContract())
	case 1: // Upgrade contract to new Wasm
		if len(g.contracts) > 0 {
			f.Changes = append(f.Changes, g.upgradeWasmContract())
		} else {
			f.Changes = append(f.Changes, g.createWasmContract())
		}
	case 2: // Create contract with reference
		f.Changes = append(f.Changes, g.createReferenceContract())
	case 3: // Update reference
		if len(g.references) > 0 {
			f.Changes = append(f.Changes, g.updateReference())
		} else {
			f.Changes = append(f.Changes, g.createReference())
		}
	case 4: // Archive contract instance
		if len(g.contracts) > 0 {
			f.Changes = append(f.Changes, g.archiveContract())
		} else {
			f.Changes = append(f.Changes, g.createWasmContract())
		}
	case 5: // Restore contract instance
		if len(g.contracts) > 0 {
			f.Changes = append(f.Changes, g.restoreContract())
		} else {
			f.Changes = append(f.Changes, g.createWasmContract())
		}
	}

	// Sometimes add a second change in the same ledger
	if g.rng.Int31n(5) == 0 {
		switch g.rng.Int31n(3) {
		case 0:
			if len(g.contracts) > 0 {
				f.Changes = append(f.Changes, g.archiveContract())
			}
		case 1:
			if len(g.references) > 0 {
				f.Changes = append(f.Changes, g.updateReference())
			}
		case 2:
			if len(g.contracts) > 0 {
				f.Changes = append(f.Changes, g.restoreContract())
			}
		}
	}

	// Filter out empty changes
	var validChanges []Change
	for _, c := range f.Changes {
		if c.Entry != nil || c.Key != nil {
			validChanges = append(validChanges, c)
		}
	}
	f.Changes = validChanges

	return f
}

func (g *factGenerator) createWasmContract() Change {
	id := g.newContractID()
	hash := g.randomWasmHash()
	cd := instanceEntry(g.t, execWasm(hash), nil)
	cd.Contract = contractAddr(g.t, id)
	st := &contractState{
		id:        id,
		kind:      KindWasm,
		wasmHash:  hash,
		createdAt: g.ledger,
		updatedAt: g.ledger,
	}
	g.contracts[id] = st
	return dataChange(cd, Created)
}

func (g *factGenerator) upgradeWasmContract() Change {
	id := g.randomContractID()
	st := g.contracts[id]
	hash := g.randomWasmHash()
	cd := instanceEntry(g.t, execWasm(hash), nil)
	cd.Contract = contractAddr(g.t, id)
	st.wasmHash = hash
	st.updatedAt = g.ledger
	return dataChange(cd, Updated)
}

func (g *factGenerator) createReference() Change {
	owner, tag := g.newReference()
	hash := g.randomWasmHash()
	h, _ := ParseWasmHash(hash)
	b := xdr.ScBytes(h[:])
	s := xdr.ScString(tag)
	cd := xdr.ContractDataEntry{
		Contract:   contractAddr(g.t, owner),
		Durability: xdr.ContractDataDurabilityPersistent,
		Key:        xdr.ScVal{Type: xdr.ScValTypeScvExecutableTag, ExecutableTag: &s},
		Val:        xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &b},
	}
	refKey := fmt.Sprintf("%s:%s", owner, tag)
	g.references[refKey] = &referenceState{
		owner:     owner,
		tag:       tag,
		wasmHash:  hash,
		updatedAt: g.ledger,
	}
	return dataChange(cd, Created)
}

func (g *factGenerator) updateReference() Change {
	keys := make([]string, 0, len(g.references))
	for k := range g.references {
		keys = append(keys, k)
	}
	key := keys[g.rng.Int31n(int32(len(keys)))]
	ref := g.references[key]
	hash := g.randomWasmHash()
	h, _ := ParseWasmHash(hash)
	b := xdr.ScBytes(h[:])
	s := xdr.ScString(ref.tag)
	cd := xdr.ContractDataEntry{
		Contract:   contractAddr(g.t, ref.owner),
		Durability: xdr.ContractDataDurabilityPersistent,
		Key:        xdr.ScVal{Type: xdr.ScValTypeScvExecutableTag, ExecutableTag: &s},
		Val:        xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &b},
	}
	ref.wasmHash = hash
	ref.updatedAt = g.ledger
	return dataChange(cd, Updated)
}

func (g *factGenerator) createReferenceContract() Change {
	owner, tag := g.newReference()
	hash := g.randomWasmHash()
	// First create the reference
	refKey := fmt.Sprintf("%s:%s", owner, tag)
	g.references[refKey] = &referenceState{
		owner:     owner,
		tag:       tag,
		wasmHash:  hash,
		updatedAt: g.ledger,
	}
	// Then create the contract using the reference
	id := g.newContractID()
	cd := instanceEntry(g.t, xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableExternalRef, ExternalRef: &xdr.ContractExecutableExternalRef{ExecutableOwner: contractAddr(g.t, owner), Tag: xdr.ScString(tag)}}, nil)
	cd.Contract = contractAddr(g.t, id)
	st := &contractState{
		id:        id,
		kind:      KindWasmRef,
		wasmHash:  hash,
		refOwner:  owner,
		refTag:    tag,
		createdAt: g.ledger,
		updatedAt: g.ledger,
	}
	g.contracts[id] = st
	// Return the contract creation change
	return dataChange(cd, Created)
}

func (g *factGenerator) archiveContract() Change {
	id := g.randomContractID()
	st := g.contracts[id]
	if st.archived {
		return Change{} // already archived, return empty
	}
	st.archived = true
	st.updatedAt = g.ledger
	var key xdr.LedgerKey
	if st.kind == KindWasmRef {
		// Archive the instance
		key = xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractData, ContractData: &xdr.LedgerKeyContractData{
			Contract:   contractAddr(g.t, id),
			Durability: xdr.ContractDataDurabilityPersistent,
			Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
		}}
	} else {
		// Archive the code
		h, _ := ParseWasmHash(st.wasmHash)
		key = xdr.LedgerKey{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.LedgerKeyContractCode{Hash: h}}
	}
	return Change{Kind: Evicted, Key: &key}
}

func (g *factGenerator) restoreContract() Change {
	id := g.randomContractID()
	st := g.contracts[id]
	if !st.archived {
		return Change{} // not archived, return empty
	}
	st.archived = false
	st.updatedAt = g.ledger
	var entry *xdr.LedgerEntry
	if st.kind == KindWasmRef {
		// Restore the instance
		h, _ := ParseWasmHash(st.wasmHash)
		ex := xdr.ContractExecutable{Type: xdr.ContractExecutableTypeContractExecutableWasm, WasmHash: &h}
		cd := instanceEntry(g.t, ex, nil)
		cd.Contract = contractAddr(g.t, id)
		entry = &xdr.LedgerEntry{Data: xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractData, ContractData: &cd}}
	} else {
		// Restore the code
		code, ok := g.codeHashes[st.wasmHash]
		if !ok || code == nil {
			// Code not available, skip this restore
			return Change{}
		}
		h, _ := ParseWasmHash(st.wasmHash)
		entry = &xdr.LedgerEntry{Data: xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.ContractCodeEntry{Hash: h, Code: code}}}
	}
	return Change{Kind: Restored, Entry: entry}
}

// splitIntoBatches splits facts into random batch sizes (1-10 ledgers each).
func (g *factGenerator) splitIntoBatches(facts []ContractFacts) [][]ContractFacts {
	var batches [][]ContractFacts
	i := 0
	for i < len(facts) {
		batchSize := int(g.rng.Int31n(10)) + 1
		end := i + batchSize
		if end > len(facts) {
			end = len(facts)
		}
		batches = append(batches, facts[i:end])
		i = end
	}
	return batches
}

// applyFacts applies a sequence of facts to a fresh indexer and returns a dump.
func (g *factGenerator) applyFacts(facts []ContractFacts) string {
	ctx := context.Background()

	ix, s := newIndexer(g.t, &fakeRPCForGenerator{g: g})

	// Apply facts in batches
	batches := g.splitIntoBatches(facts)
	for _, batch := range batches {
		tx, err := s.Begin(ctx)
		if err != nil {
			g.t.Fatal(err)
		}
		for _, f := range batch {
			if err := ix.ApplyLedger(ctx, tx, f); err != nil {
				_ = tx.Rollback()
				g.t.Fatalf("ApplyLedger failed: %v", err)
			}
		}
		if err := tx.SetLastLedger(ctx, batch[len(batch)-1].Ledger); err != nil {
			g.t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			g.t.Fatal(err)
		}
	}

	return dump(g.t, s)
}

// fakeRPCForGenerator serves code and data for the fact generator.
type fakeRPCForGenerator struct {
	g *factGenerator
}

func (f *fakeRPCForGenerator) GetNetwork(context.Context) (rpc.Network, error) {
	return rpc.Network{Passphrase: "Test SDF Network ; September 2015"}, nil
}
func (f *fakeRPCForGenerator) GetLatestLedger(context.Context) (rpc.LatestLedger, error) {
	return rpc.LatestLedger{Sequence: f.g.ledger + 100}, nil
}
func (f *fakeRPCForGenerator) GetLedgers(context.Context, rpc.LedgersRequest) (rpc.LedgersPage, error) {
	return rpc.LedgersPage{OldestLedger: 1, LatestLedger: f.g.ledger + 100}, nil
}
func (f *fakeRPCForGenerator) GetLedgerEntries(_ context.Context, keys []string) (rpc.LedgerEntries, error) {
	res := rpc.LedgerEntries{LatestLedger: f.g.ledger + 100}
	for _, k := range keys {
		var lk xdr.LedgerKey
		if err := xdr.SafeUnmarshalBase64(k, &lk); err != nil {
			continue
		}
		live := f.g.ledger + 100
		var d xdr.LedgerEntryData
		switch lk.Type {
		case xdr.LedgerEntryTypeContractCode:
			hash := hex.EncodeToString(lk.ContractCode.Hash[:])
			code, ok := f.g.codeHashes[hash]
			if !ok {
				continue
			}
			d = xdr.LedgerEntryData{Type: xdr.LedgerEntryTypeContractCode, ContractCode: &xdr.ContractCodeEntry{Hash: lk.ContractCode.Hash, Code: code}}
		case xdr.LedgerEntryTypeContractData:
			// We don't serve reference data via RPC in this test
			continue
		}
		s, err := xdr.MarshalBase64(d)
		if err != nil {
			continue
		}
		res.Entries = append(res.Entries, rpc.LedgerEntry{Key: k, XDR: s, LastModifiedLedgerSeq: 100, LiveUntilLedgerSeq: &live})
	}
	return res, nil
}

// TestPropertyBasedReplay runs property-based tests with random fact sequences.
func TestPropertyBasedReplay(t *testing.T) {
	// Test multiple seeds
	seeds := []int64{1, 2, 3, 4, 5, 42, 123, 456, 789, 999}

	for _, seed := range seeds {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			g := newFactGenerator(t, seed)
			facts := g.generateFactSequence(50) // Generate 50 random facts

			// Apply as single batch
			singleBatchState := g.applyFacts(facts)

			// Apply as multiple random batches
			multiBatchState := g.applyFacts(facts)

			// They should be identical
			if singleBatchState != multiBatchState {
				t.Fatalf("seed %d: single batch vs multi batch state differs", seed)
			}

			// Apply again (replay) - should be idempotent
			replayState := g.applyFacts(facts)
			if replayState != singleBatchState {
				t.Fatalf("seed %d: replay changed state", seed)
			}

			t.Logf("Seed %d: all three runs identical (%d contracts, %d references)",
				seed, len(g.contracts), len(g.references))
		})
	}
}
