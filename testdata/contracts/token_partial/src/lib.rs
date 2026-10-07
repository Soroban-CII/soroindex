//! SEP-41 without `burn` and `burn_from`, declaring `sep=41`.
//! Storage-compatible with token_full_sep, so a deployed token_full_sep can be upgraded to it.
#![no_std]
use soroban_sdk::{contract, contractimpl, contractmeta, Address, BytesN, Env, MuxedAddress, String};

contractmeta!(key = "sep", val = "41");

#[contract]
pub struct Token;

#[contractimpl]
impl Token {
    pub fn __constructor(env: Env, admin: Address, decimals: u32, name: String, symbol: String) {
        common::init(&env, admin, decimals, name, symbol);
    }

    pub fn mint(env: Env, to: Address, amount: i128) {
        common::mint(&env, to, amount);
    }

    pub fn upgrade(env: Env, new_wasm_hash: BytesN<32>) {
        common::upgrade(&env, new_wasm_hash);
    }

    pub fn allowance(env: Env, from: Address, spender: Address) -> i128 {
        common::allowance(&env, from, spender)
    }

    pub fn approve(env: Env, from: Address, spender: Address, amount: i128, live_until_ledger: u32) {
        common::approve(&env, from, spender, amount, live_until_ledger);
    }

    pub fn balance(env: Env, id: Address) -> i128 {
        common::balance(&env, &id)
    }

    pub fn transfer(env: Env, from: Address, to: MuxedAddress, amount: i128) {
        common::transfer(&env, from, to.address(), to.id(), amount);
    }

    pub fn transfer_from(env: Env, spender: Address, from: Address, to: Address, amount: i128) {
        common::transfer_from(&env, spender, from, to, amount);
    }

    pub fn decimals(env: Env) -> u32 {
        common::decimals(&env)
    }

    pub fn name(env: Env) -> String {
        common::name(&env)
    }

    pub fn symbol(env: Env) -> String {
        common::symbol(&env)
    }
}
