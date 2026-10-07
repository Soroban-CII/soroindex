# Demo script

Commands for the demo video. Every output below is pasted from a real run on 7 October 2026. The planned demo also shows `query --tier declared` vs `--tier inferred`, `contract --history` and `curl /v1/stats`. Those commands are not built yet, so they are not shown.

## 1. Build

```sh
git clone https://github.com/Soroban-CII/soroindex && cd soroindex
go build -o sep47idx ./cmd/sep47idx
./sep47idx version
```

```text
sep47idx dev (go1.27.1, darwin/arm64)
```

## 2. The adoption census (Phase 0)

Every mainnet contract, from Stellar's Hubble dataset, with each Wasm fetched through a public RPC:

```sh
./sep47idx phase0 --network mainnet --rpc-url https://soroban-rpc.mainnet.stellar.gateway.fm \
  --hashes report/mainnet_hashes.csv --instances report/mainnet_instances.csv --out report/
```

```text
mainnet: 156173 contracts (4042 SAC, 152131 wasm, 0 wasm_ref); 2178 hashes fetched, 3083 archived; declares any SEP: 13/2178 hashes (0.60%), 16/141977 contracts (0.01%); SEP-41 match 120 hashes; undeclared gap 109 hashes
wrote report/
```

Point at: 0.60% of live Wasm declares anything; 120 hashes match the SEP-41 interface; 109 of those declare nothing. Open `report/ADOPTION.md` to show the method and the decision row.

## 3. Build an index from a sample and find the undeclared gap

```sh
./sep47idx phase0 --network testnet --sample 200 --out /tmp/claude-501-qs-report/
./sep47idx sync   --network testnet --db ./data/testnet.db --seed /tmp/claude-501-qs-report/testnet-contracts.csv
./sep47idx gap    --network testnet --db ./data/testnet.db --sep 41
```

```text
testnet: 200 contracts (7 SAC, 188 wasm, 1 wasm_ref); 116 hashes fetched, 6 archived; declares any SEP: 0/116 hashes (0.00%), 0/182 contracts (0.00%); SEP-41 match 9 hashes; undeclared gap 9 hashes
wrote /tmp/claude-501-qs-report/
seeded 200 of 200 contracts (0 not found, 4 archived, 0 hash hints differed); fetched 125 wasm; last_ledger 5075728
Undeclared gap for SEP-41 (inferred: interface matches sep41-v0.5.2, no SEP declared): 22 contracts
CAEXK2QQJYJLQPWZKWEPJMHL73VD5PPLJVKC6WMJL3W5YMDQ2EKNMJZJ  a5f6b06ca8fb10a7f3d12ba5fae3361f04a242054757080cf14c375b88e1c898
CAT76PQMLGFABA37ETPJDKTYONMY463Z6SINVUMAM7556YKQRPYMKSKL  f345228dca59c6605789620e9ec62ff4847a0927c33dac7581a955fe746016be
...
```

Point at: the `gap` output is labelled **inferred**: these contracts match the token interface but claim nothing. The sample is random, so counts differ between runs.

## 4. The index stores exactly what the census measured

```sh
./sep47idx phase0 --network testnet --out report/ --verify-db s20.db
```

```text
check                                    phase0      store
live SAC contracts                          112        112  ok
live wasm contracts                        1842       1842  ok
measured contracts                         1775       1775  ok
hashes run by a measured contract           479        479  ok
SEP-41 match, contracts                     100        100  ok
...
```

All 18 checks print `ok`, and the command exits 0.

## 5. Show a declaration being read

```sh
stellar contract info meta --wasm testdata/wasm/token_full_sep.wasm --output json
```

```text
[{"sc_meta_v0":{"key":"sep","val":"41"}},{"sc_meta_v0":{"key":"rsver","val":"1.98.1"}},{"sc_meta_v0":{"key":"rssdkver","val":"28.0.0#48d506712f964094d14176e2f0b02afcd1054567"}},{"sc_meta_v0":{"key":"rssdk_spec_shaking","val":"2"}},{"sc_meta_v0":{"key":"cliver","val":"28.0.0#300aaf69ab100536678bdb641428b06f06b318ea"}}]
```

The `sep` entry `41` is what `contractmeta!(key = "sep", val = "41")` writes. The `token_full_sep` fixture is built from `testdata/contracts/token_full_sep`.
