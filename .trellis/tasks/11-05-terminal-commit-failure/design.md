# Terminal commit failures design

## 1. Context

`resourceOrigin` owns publication lifecycle and coordinates equivalent callers
through one deterministic resource ID. A pending generation has one waiter
channel. `Commit` currently returns terminal errors such as `storage_limit`
without changing the record or notifying that channel, so the resource ID stays
pending until the production deadline. This design closes that lifecycle gap.
(`R1`, `R2`; `.trellis/spec/backend/resource-origin.md`)

## 2. Module boundaries

| Module | Responsibility | Change |
| --- | --- | --- |
| `originManager` | Own publication record state, retained-byte accounting, waiter notification, cleanup scheduling | Terminalize commit failures |
| `resourceOriginNode` | Decode operations and map manager results to RuleGo relations | No behavioral change beyond component version |
| `resourceOriginResponse` | Project node result to HTTP | Preserve existing mapping |
| Tests and runtime fixture | Prove state, waiter, cleanup, and HTTP behavior | Add focused regressions |

The manager remains source- and media-neutral. HLS, viewer, and quality policy do
not enter this repository. (`R1`, `R5`)

## 3. Dependency direction

```text
resourceOriginNode -> originManager
resourceOriginResponse -> resourceDescriptor metadata and state
tests -> public manager/node behavior
originManager must not depend on the HLS graph
originManager must not depend on indexedMedia
```

The manager cannot depend on a graph node ID or an HLS resource key. (`R5`)

## 4. Commit flow

```text
Commit(request)
  -> validate entrypoint and current pending generation
  -> reject stale generation without changing a newer generation
  -> enforce production deadline
  -> validate parent
  -> scan staged publication
  -> check retained-byte budget
  -> inspect ready path:
       existing ready path -> terminalize conflict
       non-ErrNotExist inspection error -> terminalize publication_failed
  -> publish/rename/persist
  -> either notify ready or terminalize failed
```

Every exit after the current-generation lookup is either `ready` or `failed`.
`invalid_input` before generation lookup and `stale_generation` after lookup do
not terminalize an unrelated newer generation. (`R1`, `R4`)

## 5. Terminal failure classification

| Commit outcome | Failure kind | Notes |
| --- | --- | --- |
| production deadline | `production_timeout` | Existing behavior |
| parent no longer ready | `parent_unavailable` | Existing behavior |
| staging scan or entrypoint invalid | `invalid_publication` | New terminal path |
| retained-byte ceiling | `storage_limit` | New terminal path |
| ready path already exists | `conflict` | Existing error/status contract; direct caller stays `conflict`/HTTP 409 and the failed waiter uses `failureKind=conflict`. The old ready path is not deleted. |
| ready-path inspection fails with a non-`ErrNotExist` error | `publication_failed` | New terminal path. The direct caller keeps the original inspect-ready-path error and cause; the failed record/waiter uses this sanitized state classification. A deterministic test must cover both contracts. |
| rename publication failure | `publication_failed` | New terminal path |
| catalog persistence failure | `publication_failed` | New terminal path after rollback attempt |

The failure kind is a sanitized classification, not a filesystem path or source
error string. The caller still receives the original typed error where one
exists. `failLocked` sets `stateFailed`, queues cleanup, and calls
`notifyLocked`; its cleanup preparation error must not mask the commit cause.
(`R1`, `R2`, `R3`)

## 6. Waiter contract

`publicationWaiter` continues to carry a descriptor rather than an error. When a
generation fails,`notifyLocked` stores the failed descriptor and closes the
channel. `Acquire` returns that descriptor with nil error, preserving the
existing failed-waiter contract and tests. The caller observes:

```json
{"state":"failed","resourceId":"...","failureKind":"storage_limit"}
```

The response processor continues to map `stateFailed` to HTTP 502. The direct
commit error continues to map through the node Failure relation and
`statusForError`, so `storage_limit` remains HTTP 507. This task does not add a
new metadata field or change the public status mapping. (`R2`, `R5`)

The existing ready-path conflict remains `conflict`: the direct caller receives
`conflict`, `statusForError("conflict")` remains HTTP 409, and the waiter
descriptor uses the same `failureKind=conflict`. No `publication_conflict` kind
is introduced. A non-`ErrNotExist` ready-path inspection failure terminalizes
the current generation as `publication_failed` for state/waiter purposes; the
direct caller keeps the original inspect-ready-path error and therefore maps
through the existing untyped/internal status path. Focused tests must assert the
direct caller error class, waiter `failureKind`, failed state, and cleanup
scheduling for both branches. (`R1`, `R2`, `R3`)

## 7. Retry identity and cleanup

`failLocked` queues generation-scoped staging cleanup and notification under the
publication mutex. The failed record is terminal and is collected after cleanup
according to the existing lifecycle rules. A concurrent or later acquire of the
same key either observes the cleanup reservation as `conflict` temporarily or
creates a new pending generation with a new token. No stale generation token is
accepted. (`R4`)

The graph must not call `fail` after a terminal commit. Terminal collection has
no tombstone, so an extra `fail` can legitimately return `stale_generation`.
Producer-node failures retain the existing `fail` path. (`R4`, `R5`)

## 8. Publish-path failure handling

After `rename(staging, ready)` succeeds, a catalog persistence failure attempts
the existing rollback. If rollback succeeds, the record is terminalized as
`publication_failed` instead of remaining pending. If rollback fails, the
existing ready-path reservation remains and is extended with failed-state
notification. The original persistence error is returned in both cases.
(`R1`, `R3`, `R5`)

A ready-path conflict fails only the current generation. It does not recursively
delete an existing ready tree because that tree may belong to another lifecycle
reservation. (`R5`)

## 9. Non-goals

- No HLS TTL, session, quality, prefetch, or transcoder changes.
- No capacity eviction or retained-byte policy change.
- No tombstone or post-collection idempotency for `fail`.
- No new RuleGo relation or HTTP contract.
- No direct release or deployment by the implementer.

## 10. Assumptions and risks

- [ASSUMPTION] Failing publish-path errors is preferable to leaving a pending
  generation available for an undefined retry window.
- [ASSUMPTION] Existing tests and the API contract permit failed-waiter delivery
  with a nil error and failed descriptor.
- Cleanup failure must not suppress notification; this is an invariant, not an
  optional diagnostic.

## 11. Acceptance mapping

| PRD criterion | Design landing |
| --- | --- |
| AC1 | §4, §5, §6 |
| AC2 | §5, §6 |
| AC3 | §5 |
| AC4 | §7 |
| AC5 | §2, §5, §8 |
| AC6 | §5, §8; focused coverage must include `invalid_publication`, one rename/catalog publication failure, existing-ready-path `conflict`, and non-`ErrNotExist` ready-path inspection as `publication_failed` |
| AC7 | release identity in PRD plus implementation plan |
