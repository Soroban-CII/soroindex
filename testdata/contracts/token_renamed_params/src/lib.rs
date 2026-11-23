//! SEP-41 surface with every parameter renamed and every type correct.
//! The matcher compares types and their order, never names, so this must
//! still be a `match`.
//!
//! Matcher edge case (testdata/contracts/README.md). It exports exactly the
//! 10 SEP-41 functions and nothing else, so renaming is the only thing
//! that separates it from `token_full_sep`. It declares `sep=41`, as
//! a contract that believes it is a token would.
#![no_std]
use soroban_sdk::{contract, contractimpl, contractmeta, Address, Env, MuxedAddress, String};

contractmeta!(key = "sep", val = "41");

#[contract]
pub struct RenamedParams;

#[contractimpl]
impl RenamedParams {
    pub fn allowance(env: Env, owner: Address, delegate: Address) -> i128 {
        common::allowance(&env, owner, delegate)
    }

    pub fn approve(env: Env, owner: Address, delegate: Address, value: i128, expires_at: u32) {
        common::approve(&env, owner, delegate, value, expires_at);
    }

    pub fn balance(env: Env, account: Address) -> i128 {
        common::balance(&env, &account)
    }

    pub fn transfer(env: Env, sender: Address, recipient: MuxedAddress, value: i128) {
        common::transfer(&env, sender, recipient.address(), recipient.id(), value);
    }

    pub fn transfer_from(env: Env, delegate: Address, owner: Address, recipient: Address, value: i128) {
        common::transfer_from(&env, delegate, owner, recipient, value);
    }

    pub fn burn(env: Env, account: Address, value: i128) {
        common::burn(&env, account, value);
    }

    pub fn burn_from(env: Env, delegate: Address, owner: Address, value: i128) {
        common::burn_from(&env, delegate, owner, value);
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
