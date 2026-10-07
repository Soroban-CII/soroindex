//! Three functions unrelated to SEP-41, yet declaring `sep=41`: a false claim.
//! The declared tier lists it; the inferred tier must report a mismatch.
#![no_std]
use soroban_sdk::{contract, contractimpl, contractmeta, symbol_short, vec, Env, Symbol, Vec};

contractmeta!(key = "sep", val = "41");

#[contract]
pub struct NotToken;

#[contractimpl]
impl NotToken {
    pub fn hello(env: Env, to: Symbol) -> Vec<Symbol> {
        vec![&env, symbol_short!("Hello"), to]
    }

    pub fn add(a: u32, b: u32) -> u32 {
        a.saturating_add(b)
    }

    pub fn version() -> u32 {
        1
    }
}
