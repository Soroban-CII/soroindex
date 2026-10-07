//! Two `sep` meta entries emitted from separate modules, `41` and `40`.
//! SEP-47 says repeated entries are joined with commas, so a reader must
//! report [41, 40] with no anomaly.
#![no_std]
use soroban_sdk::{contract, contractimpl, Env};

mod token_part {
    soroban_sdk::contractmeta!(key = "sep", val = "41");
}

mod oracle_part {
    soroban_sdk::contractmeta!(key = "sep", val = "40");
}

#[contract]
pub struct MultiSep;

#[contractimpl]
impl MultiSep {
    pub fn decimals(_env: Env) -> u32 {
        7
    }

    pub fn resolution(_env: Env) -> u32 {
        300
    }
}
