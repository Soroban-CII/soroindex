//! A SEP-40 oracle price feed declaring `sep=40`.
//!
//! `PriceData`, `Asset` and the seven `PriceFeedTrait` functions are copied
//! from stellar/stellar-protocol `ecosystem/sep-0040.md`, Version 0.1.0
//! (Updated 2023-05-13), read at commit
//! 7a502f4a4ebdc9b6a8c2abf2509e74f8a385b77d. The SEP declares the two types
//! without `pub` or derives; both are added here so the contract compiles.
//! Neither changes the spec entry.
//!
//! The SEP leaves price reporting out of scope. `__constructor` and the
//! admin-gated `set_price` exist so the feed can be deployed and filled;
//! they are not part of SEP-40, and the matcher ignores functions that no
//! rule file names.
#![no_std]
use soroban_sdk::{
    contract, contracterror, contractimpl, contractmeta, contracttype, panic_with_error, Address,
    Env, Symbol, Vec,
};

contractmeta!(key = "sep", val = "40");

/// Price data for an asset at a specific timestamp
#[contracttype]
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct PriceData {
    pub price: i128,
    pub timestamp: u64,
}

#[contracttype]
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum Asset {
    Stellar(Address),
    Other(Symbol),
}

#[contracterror]
#[derive(Copy, Clone, Debug, Eq, PartialEq, PartialOrd, Ord)]
#[repr(u32)]
pub enum OracleError {
    NotInitialized = 1,
    ZeroResolution = 2,
    StaleTimestamp = 3,
}

#[contracttype]
#[derive(Clone)]
enum DataKey {
    Admin,
    Base,
    Assets,
    Decimals,
    Resolution,
    /// Every record for one asset, oldest first.
    History(Asset),
}

const DAY_IN_LEDGERS: u32 = 17_280;
const BUMP: u32 = 30 * DAY_IN_LEDGERS;
const THRESHOLD: u32 = BUMP - DAY_IN_LEDGERS;

/// The most records `set_price` keeps per asset. SEP-40 leaves retention to
/// the provider; a bound keeps one persistent entry from growing forever.
const MAX_HISTORY: u32 = 256;

fn instance<V: soroban_sdk::TryFromVal<Env, soroban_sdk::Val>>(env: &Env, key: &DataKey) -> V {
    env.storage()
        .instance()
        .extend_ttl(THRESHOLD, BUMP);
    env.storage()
        .instance()
        .get(key)
        .unwrap_or_else(|| panic_with_error!(env, OracleError::NotInitialized))
}

fn history(env: &Env, asset: &Asset) -> Vec<PriceData> {
    let key = DataKey::History(asset.clone());
    match env.storage().persistent().get::<_, Vec<PriceData>>(&key) {
        Some(h) => {
            env.storage().persistent().extend_ttl(&key, THRESHOLD, BUMP);
            h
        }
        None => Vec::new(env),
    }
}

#[contract]
pub struct Oracle;

#[contractimpl]
impl Oracle {
    pub fn __constructor(env: Env, admin: Address, base: Asset, decimals: u32, resolution: u32) {
        if resolution == 0 {
            panic_with_error!(&env, OracleError::ZeroResolution);
        }
        let s = env.storage().instance();
        s.set(&DataKey::Admin, &admin);
        s.set(&DataKey::Base, &base);
        s.set(&DataKey::Assets, &Vec::<Asset>::new(&env));
        s.set(&DataKey::Decimals, &decimals);
        s.set(&DataKey::Resolution, &resolution);
        s.extend_ttl(THRESHOLD, BUMP);
    }

    /// Records `price` for `asset`. The timestamp is trimmed to the feed's
    /// resolution, as SEP-40's Design Rationale requires. A record for the
    /// same trimmed timestamp replaces the last one; an older one is refused
    /// so history stays monotonic.
    pub fn set_price(env: Env, asset: Asset, price: i128, timestamp: u64) {
        let admin: Address = instance(&env, &DataKey::Admin);
        admin.require_auth();
        let resolution = u64::from(instance::<u32>(&env, &DataKey::Resolution));
        let ts = timestamp - timestamp % resolution;

        let mut h = history(&env, &asset);
        if h.is_empty() {
            let mut assets: Vec<Asset> = instance(&env, &DataKey::Assets);
            assets.push_back(asset.clone());
            env.storage().instance().set(&DataKey::Assets, &assets);
        }
        let record = PriceData { price, timestamp: ts };
        match h.last() {
            Some(last) if last.timestamp == ts => h.set(h.len() - 1, record),
            Some(last) if last.timestamp > ts => panic_with_error!(&env, OracleError::StaleTimestamp),
            _ => {
                if h.len() == MAX_HISTORY {
                    h.pop_front();
                }
                h.push_back(record);
            }
        }
        let key = DataKey::History(asset);
        env.storage().persistent().set(&key, &h);
        env.storage().persistent().extend_ttl(&key, THRESHOLD, BUMP);
    }

    /// Return the base asset the price is reported in
    pub fn base(env: Env) -> Asset {
        instance(&env, &DataKey::Base)
    }

    /// Return all assets quoted by the price feed
    pub fn assets(env: Env) -> Vec<Asset> {
        instance(&env, &DataKey::Assets)
    }

    /// Return the number of decimals for all assets quoted by the oracle
    pub fn decimals(env: Env) -> u32 {
        instance(&env, &DataKey::Decimals)
    }

    /// Return default tick period timeframe (in seconds)
    pub fn resolution(env: Env) -> u32 {
        instance(&env, &DataKey::Resolution)
    }

    /// Get price in base asset at specific timestamp. Unknown assets and
    /// timestamps with no record return `None`, as SEP-40 asks.
    pub fn price(env: Env, asset: Asset, timestamp: u64) -> Option<PriceData> {
        let resolution = u64::from(instance::<u32>(&env, &DataKey::Resolution));
        let ts = timestamp - timestamp % resolution;
        history(&env, &asset).iter().find(|p| p.timestamp == ts)
    }

    /// Get last N price records, newest first. `None` when the asset has no
    /// records or N is zero.
    pub fn prices(env: Env, asset: Asset, records: u32) -> Option<Vec<PriceData>> {
        let h = history(&env, &asset);
        if h.is_empty() || records == 0 {
            return None;
        }
        let mut out = Vec::new(&env);
        let mut i = h.len();
        while i > 0 && out.len() < records {
            i -= 1;
            out.push_back(h.get_unchecked(i));
        }
        Some(out)
    }

    /// Get the most recent price for an asset
    pub fn lastprice(env: Env, asset: Asset) -> Option<PriceData> {
        history(&env, &asset).last()
    }
}
