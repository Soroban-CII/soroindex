//! SEP-41 token logic shared by the fixture contracts.
//!
//! This crate deliberately has no `contractmeta!` so that every `sep` meta
//! entry in a fixture comes from that fixture's own source.
#![no_std]

use soroban_sdk::{
    contracterror, contracttype, panic_with_error, Address, BytesN, ContractExecutable, Env, String,
};
use soroban_token_sdk::events::{Approve, Burn, Mint, Transfer};

const DAY_IN_LEDGERS: u32 = 17_280;
const BUMP: u32 = 30 * DAY_IN_LEDGERS;
const THRESHOLD: u32 = BUMP - DAY_IN_LEDGERS;

#[contracterror]
#[derive(Copy, Clone, Debug, Eq, PartialEq, PartialOrd, Ord)]
#[repr(u32)]
pub enum TokenError {
    NegativeAmount = 1,
    InsufficientBalance = 2,
    InsufficientAllowance = 3,
    InvalidExpiration = 4,
    NotInitialized = 5,
}

#[contracttype]
#[derive(Clone)]
pub struct AllowanceKey {
    pub from: Address,
    pub spender: Address,
}

#[contracttype]
#[derive(Clone)]
pub struct AllowanceValue {
    pub amount: i128,
    pub live_until_ledger: u32,
}

#[contracttype]
#[derive(Clone)]
pub enum DataKey {
    Admin,
    Decimals,
    Name,
    Symbol,
    Balance(Address),
    Allowance(AllowanceKey),
}

fn bump_instance(env: &Env) {
    env.storage().instance().extend_ttl(THRESHOLD, BUMP);
}

fn check_nonnegative(env: &Env, amount: i128) {
    if amount < 0 {
        panic_with_error!(env, TokenError::NegativeAmount);
    }
}

fn admin(env: &Env) -> Address {
    env.storage()
        .instance()
        .get(&DataKey::Admin)
        .unwrap_or_else(|| panic_with_error!(env, TokenError::NotInitialized))
}

pub fn init(env: &Env, admin: Address, decimals: u32, name: String, symbol: String) {
    let s = env.storage().instance();
    s.set(&DataKey::Admin, &admin);
    s.set(&DataKey::Decimals, &decimals);
    s.set(&DataKey::Name, &name);
    s.set(&DataKey::Symbol, &symbol);
    bump_instance(env);
}

pub fn mint(env: &Env, to: Address, amount: i128) {
    check_nonnegative(env, amount);
    admin(env).require_auth();
    bump_instance(env);
    receive(env, &to, amount);
    Mint { to, to_muxed_id: None, amount }.publish(env);
}

pub fn upgrade(env: &Env, new_wasm_hash: BytesN<32>) {
    admin(env).require_auth();
    env.deployer()
        .update_current_contract(ContractExecutable::Wasm(new_wasm_hash));
}

pub fn balance(env: &Env, id: &Address) -> i128 {
    bump_instance(env);
    let key = DataKey::Balance(id.clone());
    match env.storage().persistent().get::<_, i128>(&key) {
        Some(b) => {
            env.storage().persistent().extend_ttl(&key, THRESHOLD, BUMP);
            b
        }
        None => 0,
    }
}

fn write_balance(env: &Env, id: &Address, amount: i128) {
    let key = DataKey::Balance(id.clone());
    env.storage().persistent().set(&key, &amount);
    env.storage().persistent().extend_ttl(&key, THRESHOLD, BUMP);
}

fn receive(env: &Env, id: &Address, amount: i128) {
    let b = balance(env, id);
    write_balance(env, id, b + amount);
}

fn spend(env: &Env, id: &Address, amount: i128) {
    let b = balance(env, id);
    if b < amount {
        panic_with_error!(env, TokenError::InsufficientBalance);
    }
    write_balance(env, id, b - amount);
}

/// An allowance whose live_until_ledger has passed reads as zero.
pub fn allowance(env: &Env, from: Address, spender: Address) -> i128 {
    bump_instance(env);
    let key = DataKey::Allowance(AllowanceKey { from, spender });
    match env.storage().temporary().get::<_, AllowanceValue>(&key) {
        Some(a) if a.live_until_ledger >= env.ledger().sequence() => a.amount,
        _ => 0,
    }
}

pub fn approve(env: &Env, from: Address, spender: Address, amount: i128, live_until_ledger: u32) {
    from.require_auth();
    check_nonnegative(env, amount);
    bump_instance(env);
    let seq = env.ledger().sequence();
    if amount > 0 && live_until_ledger < seq {
        panic_with_error!(env, TokenError::InvalidExpiration);
    }
    let key = DataKey::Allowance(AllowanceKey { from: from.clone(), spender: spender.clone() });
    env.storage().temporary().set(&key, &AllowanceValue { amount, live_until_ledger });
    if amount > 0 {
        env.storage().temporary().extend_ttl(&key, live_until_ledger - seq, live_until_ledger - seq);
    }
    Approve { from, spender, amount, live_until_ledger }.publish(env);
}

fn spend_allowance(env: &Env, from: &Address, spender: &Address, amount: i128) {
    let current = allowance(env, from.clone(), spender.clone());
    if current < amount {
        panic_with_error!(env, TokenError::InsufficientAllowance);
    }
    if amount == 0 {
        return;
    }
    let key = DataKey::Allowance(AllowanceKey { from: from.clone(), spender: spender.clone() });
    let mut v: AllowanceValue = env.storage().temporary().get(&key).unwrap();
    v.amount = current - amount;
    env.storage().temporary().set(&key, &v);
}

/// Moves amount from `from` to `to`. A self-transfer debits and credits the
/// same balance, so it nets to zero; a zero transfer moves nothing.
pub fn transfer(env: &Env, from: Address, to: Address, to_muxed_id: Option<u64>, amount: i128) {
    from.require_auth();
    check_nonnegative(env, amount);
    bump_instance(env);
    spend(env, &from, amount);
    receive(env, &to, amount);
    Transfer { from, to, to_muxed_id, amount }.publish(env);
}

pub fn transfer_from(env: &Env, spender: Address, from: Address, to: Address, amount: i128) {
    spender.require_auth();
    check_nonnegative(env, amount);
    bump_instance(env);
    spend_allowance(env, &from, &spender, amount);
    spend(env, &from, amount);
    receive(env, &to, amount);
    Transfer { from, to, to_muxed_id: None, amount }.publish(env);
}

pub fn burn(env: &Env, from: Address, amount: i128) {
    from.require_auth();
    check_nonnegative(env, amount);
    bump_instance(env);
    spend(env, &from, amount);
    Burn { from, amount }.publish(env);
}

pub fn burn_from(env: &Env, spender: Address, from: Address, amount: i128) {
    spender.require_auth();
    check_nonnegative(env, amount);
    bump_instance(env);
    spend_allowance(env, &from, &spender, amount);
    spend(env, &from, amount);
    Burn { from, amount }.publish(env);
}

pub fn decimals(env: &Env) -> u32 {
    bump_instance(env);
    env.storage().instance().get(&DataKey::Decimals).unwrap_or_else(|| panic_with_error!(env, TokenError::NotInitialized))
}

pub fn name(env: &Env) -> String {
    bump_instance(env);
    env.storage().instance().get(&DataKey::Name).unwrap_or_else(|| panic_with_error!(env, TokenError::NotInitialized))
}

pub fn symbol(env: &Env) -> String {
    bump_instance(env);
    env.storage().instance().get(&DataKey::Symbol).unwrap_or_else(|| panic_with_error!(env, TokenError::NotInitialized))
}
