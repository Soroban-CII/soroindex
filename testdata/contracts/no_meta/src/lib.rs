//! Source declares `sep=41`, but build-fixtures.sh strips the whole
//! contractmetav0 section from the built Wasm. The result has a spec and no
//! meta, so no tier may claim anything from meta.
#![no_std]
use soroban_sdk::{contract, contractimpl, contractmeta, Env};

contractmeta!(key = "sep", val = "41");

#[contract]
pub struct NoMeta;

#[contractimpl]
impl NoMeta {
    pub fn ping(_env: Env) -> u32 {
        1
    }
}
