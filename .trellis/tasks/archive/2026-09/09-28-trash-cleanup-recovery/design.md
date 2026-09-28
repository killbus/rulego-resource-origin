# Proposed cleanup recovery design

## Boundaries and compatibility

The manager continues to own publication lifecycle and the configured filesystem
root. Producers and RuleGo static mapping keep their current roles. Request and
response payloads and node owner/ref configuration stay compatible. Keep catalog
version 1 if existing records plus generation-specific trash names can support
recovery; justify and test any necessary format change before implementation.

maxRetainedBytes remains the ready-payload admission ceiling. This task does not
promise a total-root disk quota. Residual garbage remains observable even after
ready capacity has been released. Keep healthy expiry's immediate deletion attempt;
retry backoff applies only when that attempt fails.

## Proposed mechanism

- Give retired cleanup work an explicit lifetime independent of pending/ready
  records. Key work by resource ID, generation and owned trash path, not just the
  reusable resource ID. Discover restart residue from the owned trash directory.
- Preserve failed deletion as retryable work. Integrate its next-attempt deadline
  with the existing wake/stop scheduling, including the no-live-record case.
- Use capped backoff and bounded batches. Initial proposal: 1 s base delay, 60 s
  cap, at most 32 retired entries per batch. Values are implementation choices to
  verify with deterministic tests, not public configuration or an age guarantee.
- Ensure a consistently failing entry cannot monopolize each batch. Slow recursive
  deletion must not hold the publication mutex for the whole filesystem operation;
  publish/resolve and state reconciliation need generation-safe coordination.
- Make expiry state/catalog persistence failure recoverable. Decide the minimal
  transaction ordering with explicit fault injection before changing it. Never
  re-expose expired content just to retry, commit a stale catalog snapshot over a
  newer generation, or delete a newer ready/staging tree.
- Keep startup reconciliation's current fail-fast behavior for persistent startup
  deletion failures unless assessment identifies a concrete need to change it.
  The new runtime retry path must still recover trash left by interrupted expiry.
- Add a narrow filesystem/clock seam for deterministic faults and retry deadlines.
  Avoid a general storage backend abstraction solely for tests.

## Diagnostics

Use the existing host logging facility through a minimal manager adapter. Record
bounded cleanup-failure/recovery events with an operation kind and sanitized
resource/generation identity. Avoid URLs, headers, raw payloads and uncontrolled
filesystem-error strings. Do not add a public monitoring server or high-cardinality
metric labels. Tests should verify that persistent failure is visible without a
log on every scheduler iteration. Report any byte/count measurement as garbage
inventory, separate from ready capacity and real filesystem allocation.

## Required failure matrix

| Boundary | Required outcome |
| --- | --- |
| Rename ready to trash fails | Failure remains retryable with backoff; resource is not returned as usable after expiry; healthy records progress. |
| Catalog persistence fails after hiding bytes | Retired generation remains recoverable; no lost cleanup work or stale overwrite. |
| RemoveAll fails or partially succeeds | Remaining bytes are retried while running; deletion stays idempotent. |
| Same ID is acquired/published again | Old-generation cleanup cannot touch new-generation media or catalog state. |
| No live records or requests | Cleanup deadline still wakes the manager. |
| Permanent filesystem failure | Bounded attempts/work/diagnostics; no busy loop or starvation. |
| Restart at expiry boundaries | Reconciliation preserves valid ready resources and removes owned residue safely. |
| Shutdown with queued retries | Close terminates scheduling without leaking workers. |

## Capacity trade-off

Returning ready capacity at logical expiration preserves existing behavior but
cannot guarantee physical free space if deletion fails. Delaying capacity release
until deletion would change maxRetainedBytes semantics and still omit active
staging. Prefer an explicit separate disk-admission design if that stronger
product requirement is later requested. Document this limitation in the repair.

## Delivery and rollback

Develop on fix/trash-cleanup-recovery after planning review. Keep CI iterations on
that branch and use one final reviewed merge, following the user's preference.
Remote push/PR/release/deployment are separate from this task-creation request.
If useful, add manual CI dispatch for the branch as a validation-only change.
No production JSON or data mutation is part of planning or automated tests.
Preserve the owned-root/catalog compatibility needed to roll back to the prior
plugin; if a new format is essential, stop and revise the compatibility plan.

## Approved implementation ordering (2026-09-28)

User approved execution on fix/trash-cleanup-recovery from 2489c730cddfe36eca043e61c667878d4421ffef. Catalog remains v1.

Logical retirement changes state and releases counted ready bytes exactly once. A generation-owned work item is registered before attempting ready-to-trash rename and catalog persistence. Failed rename reserves that resource ID against reacquisition until hiding succeeds; other identities remain independent. Resolve never advertises an expired record, but native static mappings can still serve the old path while the filesystem refuses rename.

Retirement persistence failures never roll state back. The worker retries persistence only while the exact record remains current; a newer successfully persisted generation supersedes the obsolete snapshot. Generation-specific trash/staging deletion is idempotent and outside the publication mutex. Queue deadlines survive the absence of live records, use capped exponential backoff and oldest-deadline bounded batches. Startup reconstructs from v1 records and owned directories and remains fail-fast; startup expiry does not subtract bytes that were never counted. Crashes before hiding leave an elapsed ready record or terminal catalog record for reconciliation; crashes after hiding leave discoverable trash. Filesystem operations are not claimed to be power-loss atomic beyond existing catalog sync/rename behavior.

Scope: origin lifecycle plus focused cleanup implementation/tests, node logger adapter, README and resource-origin contract. No schema, quota or production changes. Main owns the task-scoped CI and integration harness.

Implementation boundary: origin.go owns logical retirement and catalog ordering;
cleanup.go owns generation-scoped retry scheduling and filesystem fault seams.
Focused tests exercise those boundaries without production data. Node wiring only
adapts sanitized cleanup events to the host logger; README and the resource-origin
contract describe recovery and physical-disk limitations. Recursive deletion runs
only after releasing the publication mutex. Close joins the worker; an already
running filesystem syscall is not cancellable and must return before Close does.

Physical removal and catalog persistence use independent due times after hiding.
Successful removal leaves catalog-only work until metadata succeeds or a new
persisted generation supersedes it. Do not repeat recursive deletion for
catalog-only retries. Catalog write failures must not withhold deletion that
could free disk space.
