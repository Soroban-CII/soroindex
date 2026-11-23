//! All 11 SEP-50 `NonFungibleToken` functions, declaring `sep=50`.
//!
//! Signatures copied from stellar/stellar-protocol `ecosystem/sep-0050.md`,
//! Version 0.1.0 (Updated 2025-03-10), read at commit
//! 7a502f4a4ebdc9b6a8c2abf2509e74f8a385b77d. SEP-50 marks none of them
//! optional. `TokenID` and `Balance` are written as `u32`: the SDK spec
//! generator treats a Rust type alias as an undefined user type, so an alias
//! would put `udt:TokenID` in the spec instead of the integer type.
//!
//! `__constructor` and `mint` are admin setup, not part of SEP-50; they
//! exist so the collection can be deployed and filled.
#![no_std]
use soroban_sdk::{contract, contractimpl, contractmeta, Address, Env, String};

contractmeta!(key = "sep", val = "50");

#[contract]
pub struct Nft;

#[contractimpl]
impl Nft {
    pub fn __constructor(env: Env, admin: Address, name: String, symbol: String, base_uri: String) {
        nft_common::init(&env, admin, name, symbol, base_uri);
    }

    pub fn mint(env: Env, to: Address) -> u32 {
        nft_common::mint(&env, to)
    }

    pub fn balance(env: Env, owner: Address) -> u32 {
        nft_common::balance(&env, owner)
    }

    pub fn owner_of(env: Env, token_id: u32) -> Address {
        nft_common::owner_of(&env, token_id)
    }

    pub fn transfer(env: Env, from: Address, to: Address, token_id: u32) {
        nft_common::transfer(&env, from, to, token_id);
    }

    pub fn transfer_from(env: Env, spender: Address, from: Address, to: Address, token_id: u32) {
        nft_common::transfer_from(&env, spender, from, to, token_id);
    }

    pub fn approve(env: Env, approver: Address, approved: Address, token_id: u32, live_until_ledger: u32) {
        nft_common::approve(&env, approver, approved, token_id, live_until_ledger);
    }

    pub fn approve_for_all(env: Env, owner: Address, operator: Address, live_until_ledger: u32) {
        nft_common::approve_for_all(&env, owner, operator, live_until_ledger);
    }

    pub fn get_approved(env: Env, token_id: u32) -> Option<Address> {
        nft_common::get_approved(&env, token_id)
    }

    pub fn is_approved_for_all(env: Env, owner: Address, operator: Address) -> bool {
        nft_common::is_approved_for_all(&env, owner, operator)
    }

    pub fn name(env: Env) -> String {
        nft_common::name(&env)
    }

    pub fn symbol(env: Env) -> String {
        nft_common::symbol(&env)
    }

    pub fn token_uri(env: Env, token_id: u32) -> String {
        nft_common::token_uri(&env, token_id)
    }
}
