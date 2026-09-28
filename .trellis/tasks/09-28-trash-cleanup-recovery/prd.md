# Assess and repair trash cleanup recovery

## Goal

Make resource-origin cleanup failures recoverable during normal service operation,
and give operators an accurate account of which limits govern retained resources
and which do not bound physical disk use.

The user requested a Trellis assessment or repair task after a read-only review of
an exported production pipeline and subsequently approved the concrete repair
scope. The task is in progress on fix/trash-cleanup-recovery.

## Background

Baseline: rulego-resource-origin v0.1.1, commit 2489c73. The reviewed production
export is E:/Desktop/sxYw0hmQDtSX.json and must remain read-only. It references an
external resource-origin owner. The user confirms this resource-origin source is
the production implementation, ../rulego-server-docker is the production runtime
packaging, and ../rulego is its server source. Source applicability is established.
The export does not embed owner limit values or a current filesystem inventory;
neither is required to establish the recovery defects.

The source audit establishes the following defects and limits. Evidence and
line anchors are consolidated in research/assessment.md.

| ID | Finding | Assessment |
| --- | --- | --- |
| F1 | expireLocked ignores trash deletion failures after changing lifecycle state and retained-byte accounting (origin.go:536). | P1: failed deletions can leave unbounded residual bytes during a long-running process. Production occurrence is unverified. |
| F2 | expiryLoop/sweep only schedule pending and ready records (origin.go:706, origin.go:749). | P1: an expired generation's residual trash has no runtime retry owner. |
| F3 | Expiry mutates in-memory state/accounting before catalog persistence; a persistence failure skips deletion (origin.go:546, origin.go:553). | Recovery gap requiring fault-injection verification, including same-ID reacquisition. |
| F4 | maxRetainedBytes measures ready payloads; staging, trash and catalog are outside that counter (origin.go:354, origin.go:382, origin.go:546). | Capacity semantics must be explicit; the existing ceiling is not a filesystem quota. |
| F5 | Initialization scans trash and fails if deletion fails (origin.go:671). | Existing recovery exists, but runtime retry cannot depend on restart. |

## Requirements

- R1: Preserve an auditable assessment of F1-F5, distinguishing source-proven
  production behavior from conditional failure scenarios and observed incidents. Document the export's parent TTL
  (6 h), child TTL (10 min), per-resource publication limit (32 MiB), and production
  deadline (5 min), without treating any of them as a trash-retention threshold.
- R2: Retry cleanup of retired generations while the manager is running, including
  when no pending/ready records or incoming requests remain. Transient failures
  must eventually release the residual bytes after the filesystem recovers.
- R3: Cleanup and catalog-write failures must preserve recoverable lifecycle
  state and generation ownership. Cleanup must not remove a newer generation,
  restore expired bytes to public visibility, double-decrement counters, or erase
  unrelated files. Assess crash boundaries as well as uninterrupted recovery.
- R4: Repeated failures must use bounded work and backoff, allow healthy resources
  to progress, stop cleanly with the manager, and produce actionable, rate-bounded
  diagnostics. Distinguish publication success from cleanup failure.
- R5: Keep existing request schemas, shared-owner references, TTL semantics,
  resource identity and ready-byte ceiling compatible. Explain residual trash
  separately from retained bytes; do not silently reinterpret maxRetainedBytes as
  a total-disk quota. Any stronger disk-admission policy requires separate design.
- R6: Verify the repair with deterministic failure injection, lifecycle regression
  tests, race checks, and the owning plugin CI/ABI gates. Update the resource-origin
  contract and README with actual cleanup guarantees and remaining limits.

## Acceptance Criteria

- [x] A1 (R1): research/assessment.md maps F1-F5 to source, records export values,
  states that no trash byte/count/age threshold exists, records the user-confirmed
  production source/runtime, and separates conditional faults from actual incidents.
- [x] A2 (R2): Deletion fails temporarily after expiry and later succeeds without
  restart, traffic, or another pending/ready record. Both files and the retired
  directory disappear, with retry scheduling demonstrated by deterministic tests.
- [x] A3 (R3): Catalog persistence failure, crash/restart boundaries, and same-ID
  new-generation publication cannot lose cleanup work, delete newer media, or
  corrupt retained-byte accounting. Expired content stays unpublished.
- [x] A4 (R4): Persistent failure does not cause a tight loop or starve healthy
  cleanup/publication; attempts and batch work are bounded, Close terminates the
  worker, and useful diagnostics are emitted without per-loop flooding.
- [x] A5 (R5): Existing maxRetainedBytes behavior and storage_limit remain tested;
  documentation explicitly distinguishes ready bytes, pending staging, residual
  trash, and total disk usage. No new trash quota is implied by the repair.
- [x] A6 (R3, R6): Restart cleanup, parent/child expiry, pending timeout, strict
  decode, waiter binding, path confinement, and protected unrelated/new-generation
  files pass regression tests; any changed catalog format has backward tests.
- [ ] A7 (R6): Formatting, go vet, unit and race tests, both architecture plugin
  builds, sidecar verification, matching-runtime load checks, and applicable
  publication/HTTP integration pass for the final implementation commit.

## Out of Scope

- Editing the production export, node pool, deployed filesystem, or runtime.
- Automatic production deletion, restart, deployment, release, or remote changes.
- New total-disk quotas, staging reservations, LRU eviction, or configurable trash
  age/byte/count thresholds in this repair.
- Catalog tombstone compaction and a general metrics service.
- Media generation, FFmpeg, HTTP serving, indexed-media changes, or repository rename.

## Execution Status

The assessment is complete at source level; no production incident is claimed.
The user approved the repair scope and task.py start has been run. Implementation
and deterministic recovery tests are complete locally. See research/acceptance-evidence.md for review and validation status. Linux CI/runtime gates remain open. Fault injection establishes
behavior after an explicit failing operation; it does not establish that normal
playback causes that operation to fail or predict a calendar time of failure.
