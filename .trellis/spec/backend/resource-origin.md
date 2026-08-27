# Resource Origin Contract

## 1. Scope / Trigger

Use this contract when changing publication identity, lifecycle, filesystem
layout, RuleGo node behavior, REST projection, or Plugin ABI packaging.
`resourceOrigin` owns publication state; producers own byte creation and
RuleGo `resource_mapping` owns HTTP byte transfer.

## 2. Signatures

The `resourceOrigin` node accepts strict JSON with one of four operations:

- `acquire(key, fingerprint, ttlMs, maxBytes, productionTimeoutMs, parentResourceId?)`
- `commit(resourceId, generation, entrypoint)`
- `fail(resourceId, generation, kind)`
- `resolve(resourceId, member?)`

Relations are `Produce`, `Success`, and `Failure`. REST endpoints may use the
`resourceOriginResponse` output processor.

## 3. Contracts

- `key + fingerprint` deterministically identifies a resource.
- Only the winning `acquire` receives `generation`, `stagingDir`, `publishBy`,
  and `maxBytes`; `resolve` never discloses producer lease fields.
- `commit` scans the issued staging tree and accepts only closed regular files.
- A ready result includes entrypoint, sorted members, aggregate size,
  publication time, absolute expiry, and a URL below `staticUrlPrefix`.
- Member inventories stay sorted, and membership checks use
  `slices.BinarySearch` rather than a hand-written comparator.
- Catalog and bytes live only below the configured owned root. A shared owner
  is referenced with `{"root":"ref://<owner-node-id>"}`.
- Builds use only the digest-pinned SDK/runtime in `plugin-abi-release.json`.

## 4. Validation & Error Matrix

| Condition | Error/state |
| --- | --- |
| Invalid JSON, ID, member, duration, or limit | `invalid_input` |
| Active identity with different policy | `conflict` |
| Wrong or completed generation | `stale_generation` |
| Traversal, symlink, special file, missing entrypoint | `invalid_publication` |
| Resource/global byte ceiling exceeded | `storage_limit` |
| Production deadline exceeded | `production_timeout` |
| Parent absent, expired, or not ready | `parent_unavailable` |
| Pure read of absent ID/member | `not_found` state |

## 5. Good / Base / Bad Cases

- Good: acquire once, producer writes and closes files under `stagingDir`,
  commit once, then resolve the entrypoint or a committed member.
- Base: concurrent equivalent acquires share one generation and wait for its
  terminal result.
- Bad: callers provide member inventories, write outside `stagingDir`, reuse a
  stale generation, or expose staging paths through a read-only resolve.

## 6. Tests Required

- Unit: strict decoding, identity/policy conflicts, concurrent waiter binding,
  atomic visibility, traversal/symlink/special-file rejection, size ceilings,
  parent expiry, cleanup, and restart reconciliation.
- CI: formatting, vet, tests with race detector, both Linux architectures,
  sidecar validation, and load in the matching runtime digest.
- Runtime: verify REST `202/307/404/410`, static `GET`, valid `206`, invalid
  `416`, `Last-Modified` conditional `304`, and restart preservation.
- RuleGo Server `v0.37.0` static mappings return `405` for `HEAD`; do not add a
  second HTTP server here to compensate for a host capability gap.

## 7. Wrong vs Correct

Wrong: add FFmpeg, YouTube, player-session, or HTTP Range logic to this plugin.

Correct: let any producer write a bounded complete output, publish it through
`resourceOrigin`, and let RuleGo's native static route serve the ready bytes.
