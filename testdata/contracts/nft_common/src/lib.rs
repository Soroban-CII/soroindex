//! SEP-50 non-fungible token logic shared by the NFT fixture contracts.
//!
//! Follows stellar/stellar-protocol `ecosystem/sep-0050.md`, Version 0.1.0
//! (Updated 2025-03-10), read at commit
//! 7a502f4a4ebdc9b6a8c2abf2509e74f8a385b77d.
//!
//! SEP-50 leaves `TokenID` and `Balance` generic ("should be an unsigned
//! integer"). Both are `u32` here, the type OpenZeppelin's Stellar NFT
//! library uses.
//!
//! Like `common`, this crate has no `contractmeta!`, so every `sep` meta
//! entry in a fixture comes from that fixture's own source.
#![no_std]

use soroban_sdk::{
    contracterror, contractevent, contracttype, panic_with_error, Address, Bytes, Env, String,
};

/// SEP-50's `TokenID`. Contract signatures and events must spell out `u32`:
/// the SDK spec generator treats an alias as an undefined user type.
pub type TokenID = u32;
/// SEP-50's `Balance`; same caveat as `TokenID`.
pub type Balance = u32;

const DAY_IN_LEDGERS: u32 = 17_280;
const BUMP: u32 = 30 * DAY_IN_LEDGERS;
const THRESHOLD: u32 = BUMP - DAY_IN_LEDGERS;

/// Longest base URI `token_uri` accepts, so the URI fits a fixed buffer.
const MAX_BASE_URI: u32 = 200;

#[contracterror]
#[derive(Copy, Clone, Debug, Eq, PartialEq, PartialOrd, Ord)]
#[repr(u32)]
pub enum NftError {
    NotInitialized = 1,
    NonExistentToken = 2,
    IncorrectOwner = 3,
    InsufficientApproval = 4,
    InvalidApprover = 5,
    InvalidLiveUntilLedger = 6,
    BaseUriTooLong = 7,
    TokenIdOverflow = 8,
}

#[contracttype]
#[derive(Clone)]
pub struct ApprovalData {
    pub approved: Address,
    pub live_until_ledger: u32,
}

#[contracttype]
#[derive(Clone)]
pub struct OperatorKey {
    pub owner: Address,
    pub operator: Address,
}

#[contracttype]
#[derive(Clone)]
pub enum DataKey {
    Admin,
    Name,
    Symbol,
    BaseUri,
    NextId,
    Owner(u32),
    Balance(Address),
    Approval(u32),
    Operator(OperatorKey),
}

// Event shapes follow SEP-50's "Events" section. The trait doc comment for
// `approve` lists a different layout (topics `[from, to]`, data
// `[token_id, live_until_ledger]`); the Events section is the more specific
// text, so it wins here.

#[contractevent(topics = ["transfer"], data_format = "single-value")]
pub struct Transfer {
    #[topic]
    pub from: Address,
    #[topic]
    pub to: Address,
    pub token_id: u32,
}

#[contractevent(topics = ["approve"], data_format = "vec")]
pub struct Approve {
    #[topic]
    pub owner: Address,
    #[topic]
    pub token_id: u32,
    pub approved: Address,
    pub live_until_ledger: u32,
}

#[contractevent(topics = ["approve_for_all"], data_format = "vec")]
pub struct ApproveForAll {
    #[topic]
    pub owner: Address,
    pub operator: Address,
    pub live_until_ledger: u32,
}

#[contractevent(topics = ["mint"], data_format = "single-value")]
pub struct Mint {
    #[topic]
    pub to: Address,
    pub token_id: u32,
}

fn bump_instance(env: &Env) {
    env.storage().instance().extend_ttl(THRESHOLD, BUMP);
}

fn get_instance<V: soroban_sdk::TryFromVal<Env, soroban_sdk::Val>>(env: &Env, key: &DataKey) -> V {
    bump_instance(env);
    env.storage()
        .instance()
        .get(key)
        .unwrap_or_else(|| panic_with_error!(env, NftError::NotInitialized))
}

fn set_persistent<V: soroban_sdk::IntoVal<Env, soroban_sdk::Val>>(env: &Env, key: &DataKey, v: &V) {
    env.storage().persistent().set(key, v);
    env.storage().persistent().extend_ttl(key, THRESHOLD, BUMP);
}

pub fn init(env: &Env, admin: Address, name: String, symbol: String, base_uri: String) {
    if base_uri.len() > MAX_BASE_URI {
        panic_with_error!(env, NftError::BaseUriTooLong);
    }
    let s = env.storage().instance();
    s.set(&DataKey::Admin, &admin);
    s.set(&DataKey::Name, &name);
    s.set(&DataKey::Symbol, &symbol);
    s.set(&DataKey::BaseUri, &base_uri);
    s.set(&DataKey::NextId, &0u32);
    bump_instance(env);
}

/// Mints the next sequential token to `to`. Admin-gated; not part of SEP-50,
/// which leaves minting to the implementation but defines its event.
pub fn mint(env: &Env, to: Address) -> TokenID {
    let admin: Address = get_instance(env, &DataKey::Admin);
    admin.require_auth();
    let id: TokenID = get_instance(env, &DataKey::NextId);
    let next = id
        .checked_add(1)
        .unwrap_or_else(|| panic_with_error!(env, NftError::TokenIdOverflow));
    env.storage().instance().set(&DataKey::NextId, &next);
    set_persistent(env, &DataKey::Owner(id), &to);
    set_persistent(env, &DataKey::Balance(to.clone()), &(balance(env, to.clone()) + 1));
    Mint { to, token_id: id }.publish(env);
    id
}

pub fn balance(env: &Env, owner: Address) -> Balance {
    bump_instance(env);
    let key = DataKey::Balance(owner);
    match env.storage().persistent().get::<_, Balance>(&key) {
        Some(b) => {
            env.storage().persistent().extend_ttl(&key, THRESHOLD, BUMP);
            b
        }
        None => 0,
    }
}

/// Panics for a token that does not exist, as SEP-50 specifies.
pub fn owner_of(env: &Env, token_id: TokenID) -> Address {
    bump_instance(env);
    let key = DataKey::Owner(token_id);
    let owner: Address = env
        .storage()
        .persistent()
        .get(&key)
        .unwrap_or_else(|| panic_with_error!(env, NftError::NonExistentToken));
    env.storage().persistent().extend_ttl(&key, THRESHOLD, BUMP);
    owner
}

/// Moves `token_id` and clears its approval, as SEP-50 requires.
fn move_token(env: &Env, from: Address, to: Address, token_id: TokenID) {
    if owner_of(env, token_id) != from {
        panic_with_error!(env, NftError::IncorrectOwner);
    }
    env.storage().persistent().remove(&DataKey::Approval(token_id));
    if from != to {
        set_persistent(env, &DataKey::Balance(from.clone()), &(balance(env, from.clone()) - 1));
        set_persistent(env, &DataKey::Balance(to.clone()), &(balance(env, to.clone()) + 1));
        set_persistent(env, &DataKey::Owner(token_id), &to);
    }
    Transfer { from, to, token_id }.publish(env);
}

pub fn transfer(env: &Env, from: Address, to: Address, token_id: TokenID) {
    from.require_auth();
    move_token(env, from, to, token_id);
}

/// `spender` must be the owner, an unexpired operator of the owner, or the
/// unexpired approved account for `token_id`.
pub fn transfer_from(env: &Env, spender: Address, from: Address, to: Address, token_id: TokenID) {
    spender.require_auth();
    let owner = owner_of(env, token_id);
    let allowed = spender == owner
        || is_approved_for_all(env, owner.clone(), spender.clone())
        || get_approved(env, token_id) == Some(spender);
    if !allowed {
        panic_with_error!(env, NftError::InsufficientApproval);
    }
    move_token(env, from, to, token_id);
}

pub fn approve(env: &Env, approver: Address, approved: Address, token_id: TokenID, live_until_ledger: u32) {
    approver.require_auth();
    let owner = owner_of(env, token_id);
    if approver != owner && !is_approved_for_all(env, owner.clone(), approver.clone()) {
        panic_with_error!(env, NftError::InvalidApprover);
    }
    let seq = env.ledger().sequence();
    let key = DataKey::Approval(token_id);
    // Approving one's own address clears the approval (SEP-50).
    if approved == owner {
        env.storage().persistent().remove(&key);
    } else {
        if live_until_ledger < seq {
            panic_with_error!(env, NftError::InvalidLiveUntilLedger);
        }
        set_persistent(env, &key, &ApprovalData { approved: approved.clone(), live_until_ledger });
    }
    Approve { owner, token_id, approved, live_until_ledger }.publish(env);
}

/// A `live_until_ledger` of 0 revokes the operator (SEP-50).
pub fn approve_for_all(env: &Env, owner: Address, operator: Address, live_until_ledger: u32) {
    owner.require_auth();
    let key = DataKey::Operator(OperatorKey { owner: owner.clone(), operator: operator.clone() });
    if live_until_ledger == 0 {
        env.storage().persistent().remove(&key);
    } else {
        if live_until_ledger < env.ledger().sequence() {
            panic_with_error!(env, NftError::InvalidLiveUntilLedger);
        }
        set_persistent(env, &key, &live_until_ledger);
    }
    ApproveForAll { owner, operator, live_until_ledger }.publish(env);
}

/// Panics for a token that does not exist; an expired approval reads as none.
pub fn get_approved(env: &Env, token_id: TokenID) -> Option<Address> {
    owner_of(env, token_id);
    match env.storage().persistent().get::<_, ApprovalData>(&DataKey::Approval(token_id)) {
        Some(a) if a.live_until_ledger >= env.ledger().sequence() => Some(a.approved),
        _ => None,
    }
}

pub fn is_approved_for_all(env: &Env, owner: Address, operator: Address) -> bool {
    bump_instance(env);
    let key = DataKey::Operator(OperatorKey { owner, operator });
    match env.storage().persistent().get::<_, u32>(&key) {
        Some(until) => until >= env.ledger().sequence(),
        None => false,
    }
}

pub fn name(env: &Env) -> String {
    get_instance(env, &DataKey::Name)
}

pub fn symbol(env: &Env) -> String {
    get_instance(env, &DataKey::Symbol)
}

/// `base_uri` followed by the decimal token id. Panics for a token that does
/// not exist, as SEP-50 specifies.
pub fn token_uri(env: &Env, token_id: TokenID) -> String {
    owner_of(env, token_id);
    let base: String = get_instance(env, &DataKey::BaseUri);
    let mut buf = [0u8; MAX_BASE_URI as usize];
    let n = base.len() as usize;
    base.copy_into_slice(&mut buf[..n]);
    let mut uri = Bytes::from_slice(env, &buf[..n]);
    let mut digits = [0u8; 10];
    let mut i = digits.len();
    let mut v = token_id;
    loop {
        i -= 1;
        digits[i] = b'0' + (v % 10) as u8;
        v /= 10;
        if v == 0 {
            break;
        }
    }
    uri.extend_from_slice(&digits[i..]);
    let mut out = [0u8; MAX_BASE_URI as usize + 10];
    let len = uri.len() as usize;
    uri.copy_into_slice(&mut out[..len]);
    String::from_bytes(env, &out[..len])
}
