//! A SEP-56 tokenized vault declaring `sep=41,56`.
//!
//! The 17 `TokenizedVault` functions and the `Deposit`/`Withdraw` events are
//! copied from stellar/stellar-protocol `ecosystem/sep-0056.md`, Version
//! 0.1.2 (Updated 2026-11-16, Final Comment Period), read at commit
//! 7a502f4a4ebdc9b6a8c2abf2509e74f8a385b77d.
//!
//! SEP-56 declares `trait TokenizedVault: TokenInterface` and says "the vault
//! itself must also comply with SEP-41", so the vault's shares are a SEP-41
//! token and the contract also exports all 10 SEP-41 functions (from
//! `common`, as in `token_full_sep`). That is why it declares 41 as well
//! as 56.
//!
//! SEP-56's `mint(shares, receiver, from, operator) -> i128` takes the name
//! the token fixtures use for their admin mint. This vault therefore has no
//! admin mint: shares only come from `deposit` and `mint`.
//!
//! Conversions use a virtual share and a virtual asset (`+ 1` on both sides),
//! the ERC-4626 defence against the first-depositor inflation attack.
#![no_std]
use soroban_sdk::{
    contract, contracterror, contractevent, contractimpl, contractmeta, contracttype,
    panic_with_error, token, Address, Env, MuxedAddress, String,
};

contractmeta!(key = "sep", val = "41,56");

#[contracterror]
#[derive(Copy, Clone, Debug, Eq, PartialEq, PartialOrd, Ord)]
#[repr(u32)]
pub enum VaultError {
    NotInitialized = 101,
    NegativeAmount = 102,
    Overflow = 103,
    ExceedsMax = 104,
}

#[contracttype]
#[derive(Clone)]
enum VaultKey {
    Asset,
    TotalSupply,
}

/// The deposit event is emitted when underlying assets are deposited into
/// the vault in exchange for shares.
#[contractevent]
pub struct Deposit {
    #[topic]
    pub operator: Address,
    #[topic]
    pub from: Address,
    #[topic]
    pub receiver: Address,
    pub assets: i128,
    pub shares: i128,
}

/// The withdraw event is emitted when shares are exchanged back for
/// underlying assets and assets are withdrawn from the vault.
#[contractevent]
pub struct Withdraw {
    #[topic]
    pub operator: Address,
    #[topic]
    pub receiver: Address,
    #[topic]
    pub owner: Address,
    pub assets: i128,
    pub shares: i128,
}

#[derive(Clone, Copy)]
enum Rounding {
    Down,
    Up,
}

fn check_nonnegative(env: &Env, amount: i128) {
    if amount < 0 {
        panic_with_error!(env, VaultError::NegativeAmount);
    }
}

/// `x * y / d`, rounded as asked. All three are non-negative here and `d`
/// is at least 1.
fn mul_div(env: &Env, x: i128, y: i128, d: i128, r: Rounding) -> i128 {
    let p = x
        .checked_mul(y)
        .unwrap_or_else(|| panic_with_error!(env, VaultError::Overflow));
    let q = p / d;
    match r {
        Rounding::Up if p % d != 0 => q + 1,
        _ => q,
    }
}

fn asset(env: &Env) -> Address {
    env.storage()
        .instance()
        .get(&VaultKey::Asset)
        .unwrap_or_else(|| panic_with_error!(env, VaultError::NotInitialized))
}

fn total_supply(env: &Env) -> i128 {
    env.storage().instance().get(&VaultKey::TotalSupply).unwrap_or(0)
}

fn set_total_supply(env: &Env, v: i128) {
    env.storage().instance().set(&VaultKey::TotalSupply, &v);
}

fn total_assets(env: &Env) -> i128 {
    token::TokenClient::new(env, &asset(env)).balance(&env.current_contract_address())
}

fn to_shares(env: &Env, assets: i128, r: Rounding) -> i128 {
    mul_div(env, assets, total_supply(env) + 1, total_assets(env) + 1, r)
}

fn to_assets(env: &Env, shares: i128, r: Rounding) -> i128 {
    mul_div(env, shares, total_assets(env) + 1, total_supply(env) + 1, r)
}

/// `from` must authorize unless it is `operator`, whose auth is already
/// required.
fn require_party(operator: &Address, party: &Address) {
    if party != operator {
        party.require_auth();
    }
}

fn do_deposit(env: &Env, assets: i128, shares: i128, receiver: Address, from: Address, operator: Address) {
    token::TokenClient::new(env, &asset(env)).transfer(&from, &env.current_contract_address(), &assets);
    common::receive(env, &receiver, shares);
    set_total_supply(env, total_supply(env) + shares);
    Deposit { operator, from, receiver, assets, shares }.publish(env);
}

/// When `operator` is not `owner`, it spends `owner`'s share allowance, as
/// ERC-4626 `withdraw`/`redeem` do.
fn do_withdraw(env: &Env, assets: i128, shares: i128, receiver: Address, owner: Address, operator: Address) {
    if operator != owner {
        common::spend_allowance(env, &owner, &operator, shares);
    }
    common::spend(env, &owner, shares);
    set_total_supply(env, total_supply(env) - shares);
    token::TokenClient::new(env, &asset(env)).transfer(&env.current_contract_address(), &receiver, &assets);
    Withdraw { operator, receiver, owner, assets, shares }.publish(env);
}

#[contract]
pub struct Vault;

#[contractimpl]
impl Vault {
    /// Share decimals equal the underlying asset's decimals.
    pub fn __constructor(env: Env, asset: Address, name: String, symbol: String) {
        let decimals = token::TokenClient::new(&env, &asset).decimals();
        common::init(&env, env.current_contract_address(), decimals, name, symbol);
        env.storage().instance().set(&VaultKey::Asset, &asset);
        set_total_supply(&env, 0);
    }

    // SEP-41 (the share token).

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

    /// Burning shares lowers total supply and leaves the assets in the vault,
    /// so remaining shares are worth more.
    pub fn burn(env: Env, from: Address, amount: i128) {
        common::burn(&env, from, amount);
        set_total_supply(&env, total_supply(&env) - amount);
    }

    pub fn burn_from(env: Env, spender: Address, from: Address, amount: i128) {
        common::burn_from(&env, spender, from, amount);
        set_total_supply(&env, total_supply(&env) - amount);
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

    // SEP-56.

    pub fn total_supply(env: Env) -> i128 {
        total_supply(&env)
    }

    pub fn query_asset(env: Env) -> Address {
        asset(&env)
    }

    pub fn total_assets(env: Env) -> i128 {
        total_assets(&env)
    }

    pub fn convert_to_shares(env: Env, assets: i128) -> i128 {
        check_nonnegative(&env, assets);
        to_shares(&env, assets, Rounding::Down)
    }

    pub fn convert_to_assets(env: Env, shares: i128) -> i128 {
        check_nonnegative(&env, shares);
        to_assets(&env, shares, Rounding::Down)
    }

    pub fn max_deposit(_env: Env, _receiver: Address) -> i128 {
        i128::MAX
    }

    pub fn preview_deposit(env: Env, assets: i128) -> i128 {
        check_nonnegative(&env, assets);
        to_shares(&env, assets, Rounding::Down)
    }

    pub fn deposit(env: Env, assets: i128, receiver: Address, from: Address, operator: Address) -> i128 {
        operator.require_auth();
        require_party(&operator, &from);
        check_nonnegative(&env, assets);
        let shares = to_shares(&env, assets, Rounding::Down);
        do_deposit(&env, assets, shares, receiver, from, operator);
        shares
    }

    pub fn max_mint(_env: Env, _receiver: Address) -> i128 {
        i128::MAX
    }

    pub fn preview_mint(env: Env, shares: i128) -> i128 {
        check_nonnegative(&env, shares);
        to_assets(&env, shares, Rounding::Up)
    }

    pub fn mint(env: Env, shares: i128, receiver: Address, from: Address, operator: Address) -> i128 {
        operator.require_auth();
        require_party(&operator, &from);
        check_nonnegative(&env, shares);
        let assets = to_assets(&env, shares, Rounding::Up);
        do_deposit(&env, assets, shares, receiver, from, operator);
        assets
    }

    pub fn max_withdraw(env: Env, owner: Address) -> i128 {
        to_assets(&env, common::balance(&env, &owner), Rounding::Down)
    }

    pub fn preview_withdraw(env: Env, assets: i128) -> i128 {
        check_nonnegative(&env, assets);
        to_shares(&env, assets, Rounding::Up)
    }

    pub fn withdraw(env: Env, assets: i128, receiver: Address, owner: Address, operator: Address) -> i128 {
        operator.require_auth();
        check_nonnegative(&env, assets);
        if assets > Self::max_withdraw(env.clone(), owner.clone()) {
            panic_with_error!(&env, VaultError::ExceedsMax);
        }
        let shares = to_shares(&env, assets, Rounding::Up);
        do_withdraw(&env, assets, shares, receiver, owner, operator);
        shares
    }

    pub fn max_redeem(env: Env, owner: Address) -> i128 {
        common::balance(&env, &owner)
    }

    pub fn preview_redeem(env: Env, shares: i128) -> i128 {
        check_nonnegative(&env, shares);
        to_assets(&env, shares, Rounding::Down)
    }

    pub fn redeem(env: Env, shares: i128, receiver: Address, owner: Address, operator: Address) -> i128 {
        operator.require_auth();
        check_nonnegative(&env, shares);
        if shares > common::balance(&env, &owner) {
            panic_with_error!(&env, VaultError::ExceedsMax);
        }
        let assets = to_assets(&env, shares, Rounding::Down);
        do_withdraw(&env, assets, shares, receiver, owner, operator);
        assets
    }
}
