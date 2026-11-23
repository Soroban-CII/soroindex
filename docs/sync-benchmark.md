# Sync Throughput Benchmark

This document records the sync throughput benchmark results for the soroindex project.

## Benchmark Configuration

- **Command**: `go test -bench=BenchmarkSyncThroughput ./internal/ingest/ -benchtime=5s -benchmem`
- **Go version**: go1.27.4
- **OS/Arch**: linux/amd64
- **CPU**: Intel(R) Xeon(R) Platinum 8573C (2 CPUs)
- **Date**: 2026-11-21
- **Test corpus**: 2 recorded testnet ledgers (5062907, 5062908) from `internal/rpc/testdata/getLedgers.json`

## Results

| Metric | Value |
|--------|-------|
| Ledgers/second | 790.92 |
| Facts/second | 7,118.25 |
| Average time per batch | 2.53 ms |
| Memory per operation | 4,706,137 B/op |
| Allocations per operation | 34,814 allocs/op |

## Top 3 CPU Hotspots

Based on CPU profiling (`go tool pprof -text cpu.prof`):

1. **encoding/base64.(*decoder).Read** - 6.63% (1.58s cumulative, 21.82%)
   - Base64 decoding of ledger metadata XDR from RPC responses

2. **runtime.memmove** - 4.70%
   - Memory copying during XDR decoding

3. **encoding/base64.(*Encoding).Decode** - 3.59% (0.40s cumulative, 5.52%)
   - Base64 decoding of XDR data

4. **encoding/base64.(*Encoding).Encode** - 3.59% (0.27s cumulative, 3.73%)
   - Base64 encoding for RPC request/response

5. **github.com/stellar/go-stellar-sdk/xdr.(*ScVal).DecodeFrom** - 0.83% (2.14s cumulative, 29.56%)
   - XDR decoding of ScVal types (heavily used in ledger entry processing)

Note: The cumulative percentage for `(*ScVal).DecodeFrom` is 29.56%, making it the largest single contributor when considering the full call chain.

## Top 3 Memory Allocation Hotspots

Based on memory profiling (`go tool pprof -text -sample_index=alloc_space mem.prof`):

1. **bytes.growSlice** - 23.28% (591.05 MB)
   - Slice growth during XDR decoding

2. **encoding/base64.(*Encoding).EncodeToString** - 17.02% (432.10 MB)
   - Base64 encoding of XDR data

3. **github.com/stellar/go-stellar-sdk/xdr.(*ScMap).DecodeFrom** - 16.24% (412.30 MB flat, 26.99% cumulative)
   - Decoding of ScMap (key-value maps in contract events/storage)

4. **github.com/stellar/go-stellar-sdk/xdr.(*ScVec).DecodeFrom** - 12.41% (315.10 MB flat, 25.02% cumulative)
   - Decoding of ScVec (vectors/arrays in XDR)

5. **github.com/stellar/go-stellar-sdk/xdr.(*ContractEventV0).DecodeFrom** - 5.85% (148.56 MB flat, 12.12% cumulative)
   - Contract event decoding

## Observations

1. **Base64 encoding/decoding** dominates both CPU and memory profiles. This is expected as the RPC layer uses base64 for XDR transport.

2. **XDR decoding** (especially ScVal, ScMap, ScVec, ContractEvent) is the primary work in the hot path. This is the core of the indexer's job.

3. **Memory allocations** are high (~4.7 MB/op) due to the repeated decoding of large XDR structures. The benchmark only processes 2 ledgers per iteration, so this reflects the per-batch cost.

4. **SQLite operations** (modernc.org/sqlite) appear in both profiles but are not the primary bottleneck at this scale.

## Comparison with Production Requirements

Mainnet produces ~1 ledger every ~5 seconds. At 790 ledgers/s, the indexer can easily keep up with mainnet throughput even with a single CPU core. The current bottleneck is XDR/base64 processing, not database writes.

## Running the Benchmark

```bash
# Run benchmark with memory stats
go test -bench=BenchmarkSyncThroughput ./internal/ingest/ -benchtime=10s -benchmem

# Generate CPU profile
go test -bench=BenchmarkSyncThroughput ./internal/ingest/ -benchtime=10s -cpuprofile=cpu.prof

# Generate memory profile
go test -bench=BenchmarkSyncThroughput ./internal/ingest/ -benchtime=10s -memprofile=mem.prof

# Analyze profiles
go tool pprof -text cpu.prof
go tool pprof -text -sample_index=alloc_space mem.prof
```