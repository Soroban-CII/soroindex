//! SEP-41 surface plus a second `balance` spec entry from another module.
//!
//! `balance` cannot be exported twice (the linker refuses a duplicate
//! symbol), but a module can add spec entries with `contractspecfn`, as an
//! interface crate does. The spec then holds two `balance` entries, one
//! correct and one returning `u64`, and stellar-cli warns "spec entry
//! 'balance' is defined more than once". A reader cannot tell which entry
//! describes the export, so the matcher must never count `balance` as ok.
//!
//! Matcher edge case (testdata/contracts/README.md). It exports exactly the
//! 10 SEP-41 functions and nothing else, so the extra spec entry is the
//! only thing that separates it from a match. It declares `sep=41`, as
//! a contract that believes it is a token would.
#![no_std]
use soroban_sdk::{contract, contractimpl, contractmeta, Address, Env, MuxedAddress, String};

contractmeta!(key = "sep", val = "41");

#[contract]
pub struct DuplicateFn;

#[contractimpl]
impl DuplicateFn {
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

mod balance_v2 {
    use soroban_sdk::{contractspecfn, Address, Env};

    /// Spec-only: names the `balance` export a second time, with a different
    /// output type. Nothing implements it.
    pub struct BalanceV2Spec;

    #[contractspecfn(name = "BalanceV2Spec")]
    #[allow(dead_code)] // Spec-only on purpose: the trait exists to emit the entry.
    pub trait BalanceV2 {
        fn balance(env: Env, id: Address) -> u64;
    }
}
