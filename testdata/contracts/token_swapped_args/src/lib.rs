//! SEP-41 surface where `transfer` takes `(from, amount, to)` instead of
//! `(from, to, amount)`.
//!
//! Matcher edge case (testdata/contracts/README.md). It exports exactly the
//! 10 SEP-41 functions and nothing else, so the single deviation below is
//! the only thing that separates it from a match. It declares `sep=41`, as
//! a contract that believes it is a token would.
#![no_std]
use soroban_sdk::{contract, contractimpl, contractmeta, Address, Env, MuxedAddress, String};

contractmeta!(key = "sep", val = "41");

#[contract]
pub struct SwappedArgs;

#[contractimpl]
impl SwappedArgs {
    pub fn allowance(env: Env, from: Address, spender: Address) -> i128 {
        common::allowance(&env, from, spender)
    }

    pub fn approve(env: Env, from: Address, spender: Address, amount: i128, live_until_ledger: u32) {
        common::approve(&env, from, spender, amount, live_until_ledger);
    }

    pub fn balance(env: Env, id: Address) -> i128 {
        common::balance(&env, &id)
    }

    /// Wrong on purpose: SEP-41 orders the parameters `(from, to, amount)`.
    pub fn transfer(env: Env, from: Address, amount: i128, to: MuxedAddress) {
        common::transfer(&env, from, to.address(), to.id(), amount);
    }

    pub fn transfer_from(env: Env, spender: Address, from: Address, to: Address, amount: i128) {
        common::transfer_from(&env, spender, from, to, amount);
    }

    pub fn burn(env: Env, from: Address, amount: i128) {
        common::burn(&env, from, amount);
    }

    pub fn burn_from(env: Env, spender: Address, from: Address, amount: i128) {
        common::burn_from(&env, spender, from, amount);
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
