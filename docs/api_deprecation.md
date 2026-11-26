# /v1 Compatibility and Deprecation Policy

This document describes the policy for what may change in the `/v1` API and what may not,
along with the deprecation process for anything that must change. Integrators need to know
what they can rely on across releases.

## Guiding principle

**CLAUDE.md §5.11 is the internal source of truth.** The public API surface is fixed:
response shapes, endpoint paths, query parameter names, and trust-tier semantics must not
change in a breaking way without a deprecation cycle. New fields and new endpoints are
always allowed; removing or renaming fields, changing tier semantics, or changing
response shapes are breaking changes.

## Allowed changes (non-breaking)

These changes may be made without a deprecation period:

| Change | Example |
|--------|---------|
| Adding new top-level fields to a response object | Adding a new field to the contract detail JSON |
| Adding new endpoints | Adding `GET /v1/stats/toolchains` |
| Adding new query parameters (optional) | Adding a new filter that defaults to existing behavior |
| Adding new enum values (with older value still accepted) | Adding a new SEP number to a supported list |
| Changing internal implementation without affecting API | Refactoring the matcher algorithm |

**Deprecation notice**: None required for these changes.

## Breaking changes (require deprecation cycle)

These changes are breaking and must follow the deprecation process below:

| Change type | Example | Migration path |
|---|---|---|
| **Removing a field** from a response | Removing `exec_ref` from contract detail | Deprecate in v, remove in v+1 |
| **Renaming a field** in a response | Renaming `sep` to `declared_sep` | Deprecate old name, add new name |
| **Changing tier semantics** | Presenting `inferred` as `declared` | Deprecate old behavior, document new |
| **Changing endpoint path** | Moving `/v1/contracts` to `/v1/contract/list` | Deprecate old path, add new path |
| **Changing query parameter names** | Renaming `implements` to `seps` | Deprecate old param, add new param |
| **Changing error code set** | Adding/removing error codes | Document new codes, keep old ones |

## Deprecation process

### 1. Announce deprecation

When a breaking change is planned:

- **CHANGELOG entry**: Add an entry to `CHANGELOG.md` under a version header (e.g., `## Unreleased` or `## v2.0.0`) describing the change, why it is needed, and the migration path.
- **HTTP header**: Deprecation warnings on the affected endpoint include a `Deprecation` header with the version in which the change will take effect, e.g.: `Deprecation: will remove in v2.0.0`.
- **Minimum notice**: 6 months advance notice before a breaking change takes effect. For minor releases (< 6 months away), a `Warning` header with `max-age=0` must be included.

### 2. Implement the change

After the deprecation period:

- The old field/endpoint/behavior is removed.
- The new field/endpoint/replacement takes its place.
- The CHANGELOG records the removal under the release version.
- API consumers who have not migrated receive HTTP 404 (for removed endpoints) or 400 with error code `field_not_found` (for removed fields).

### 3. Version annotations in responses

Responses that carry deprecation-sensitive data include a `deprecated` boolean field
(or `deprecated: true` in nested objects) that indicates whether the data may change
in a future breaking release. This field is:

- `false` for data that will never change (trust tier, contract ID, parser version)
- `true` for data that may change (individual SEP declarations, matched variants,
  toolchain bucket counts) — always check the current value, do not cache it.

## Examples

### Adding a new endpoint (allowed, no deprecation)

```http
GET /v1/health
```
New endpoint `GET /v1/health` added. No deprecation needed. CORS, rate limits, and
pagination rules apply as for all endpoints.

### Removing a field (breaking, deprecation cycle)

1. **v1.0.0**: Add deprecation header and `deprecated: true` to the field in responses.
   CHANGELOG: `## Unreleased: Deprecating wasm_hash_alt field; will remove in v2.0.0`.

2. **v1.5.0** (6 months later): Remove `wasm_hash_alt` field from the handler.
   CHANGELOG: `## v1.5.0: Removed wasm_hash_alt field from contract detail`.

3. **v2.0.0**: The field is gone from the response. Clients using it get `400`
   with `{"error":{"code":"bad_request","message":"field wasm_hash_alt is deprecated
   and has been removed"}}`.

### Changing tier semantics (breaking, deprecation cycle)

1. **v0.9.0**: Add `deprecated: true` to the `inferred` tier explanation in API docs.
   CHANGELOG: `## Unreleased: `inferred` status semantics will change in v1.0.0;
   reviewed by maintainers.`.

2. **v1.0.0**: The `declared` tier now also includes contracts that previously only
   matched via the `inferred` tier. Documentation updated. Deprecation notice sent
   to registered API consumers.

3. **v1.1.0** (6 months later): The old `inferred`-only presentation is removed from
   the `/v1/seps` and `/v1/contracts` endpoints. CHANGELOG: `## v1.1.0: `declared`
   tier now includes previously-inferred-only contracts.`.

## Linked documentation

- **README.md**: References this policy in the "Trust tier" section with a link.
- **docs/api.md**: Includes a "Compatibility" sidebar note linking to this page.
- **CHANGELOG.md**: All deprecation entries and release upgrades are recorded here.

## Contact

For questions about the deprecation policy or to discuss a planned breaking change,
open a GitHub issue or contact the maintainers. Maintainers review all breaking change
proposals within 48 hours.