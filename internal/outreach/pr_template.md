<!--
Reviewed pull-request body for one undeclared-gap Wasm. `sep47idx gap
--outreach` fills the placeholders. The match is inferred from function
types; neither this template nor the index claims the contract behaves as
the SEP describes. Edit it here, not in the generator.
-->

## Declare SEP-{{SEP}} in the contract metadata

`{{WASM_HASH}}` matches the SEP-{{SEP}} interface in the soroindex rule file
(`{{RULESET}}`), but its metadata declares no SEP. That match is inferred
from the function types in its `contractspecv0` section; it is not a claim
about behavior.

{{CONTRACT_COUNT}} live contract(s) run this code:

{{CONTRACTS}}

Add this line to the contract crate:

```rust
{{SEP_LINE}}
```

Then run `sep47idx wasm {{WASM_HASH}}` and confirm it prints
`Declared SEP-{{SEP}}`.

- [ ] Add the `contractmeta!` line
- [ ] Rebuild and redeploy
- [ ] Confirm `Declared SEP-{{SEP}}` with `sep47idx wasm`
