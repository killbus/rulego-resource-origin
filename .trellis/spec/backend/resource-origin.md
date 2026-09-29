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
- Retirement releases counted ready bytes once and registers cleanup by resource
  ID plus generation. Ready-to-trash hiding is serialized with publication;
  recursive generation-owned trash/staging removal runs outside the mutex.
- Once hidden, physical deletion proceeds despite catalog errors. Retry persistence
  only while the exact retired record is current; never overwrite a new generation.
- Retired work wakes the scheduler without live records/requests. Use capped 1 s to
  60 s backoff and at most 32 oldest-due entries per deletion batch. This bounds
  deletion batches, not all record scanning or initial metadata work.
- Failure/recovery log samples contain sanitized IDs, operation, error class,
  failure count and pending work count. Emit at most one event/second/manager,
  outside the mutex. Pending includes catalog-only work, not a trash byte measure.
  Register scan failures before emitting their first event so Pending includes
  the failed root. Clear recovered scan backlog after a successful full pass;
  stale diagnostics must not retain pending counts or oldest-wait age forever.
- Startup remains fail-fast on cleanup errors and reads catalog v1. Close cancels
  timers and joins the worker, waiting for any in-flight filesystem syscall.
- A root has one manager owner; there is no cross-process locking. Close joins
  the cleanup worker, not producer calls; callers must finish their own work.
- Failed/expired records have no retention interval. After payload removal,
  terminal persistence and waiter delivery finish, collectTerminalLocked checks
  the exact record pointer and unlinks catalog/<id>.json under the publication
  mutex, then drops the memory record. Unlink failure retains retry responsibility.
  A pending child does not keep its terminal parent alive; commit still returns
  parent_unavailable after the parent record is collected.
- Idle residue reconciliation rotates catalog, ready, staging and trash, observing
  at most 32 directory entries per one-second opportunity with bounded cursors.
  Protect pending generations, live ready paths, hide reservations and queued or
  in-flight cleanup paths. Claim unknown entries by rename into a unique
  trash/.gc-*/payload; perform recursive deletion outside the publication mutex.
  Catalog observation/claim shares the mutex with temporary catalog writes.
- Validate managed directories and their ancestors without following symlinks,
  including before creating missing directories. Startup rejects abnormal managed
  paths and ID-shaped catalog symlinks/special files; unknown names and directory
  entries are reclaimable residue. A symlink residue may be removed, never its
  target. Lstat checks do not claim protection against hostile concurrent path
  replacement by another process.
- Catalog v1 remains readable; GC does not preserve terminal history for rollback.
  Once collected, resolve returns not_found (REST 404), so callers cannot depend
  on a permanent expired/410 response. TTL remains absolute and is not refreshed.
- maxRetainedBytes covers ready payloads only; staging, trash, persistent catalog
  records and filesystem overhead are excluded. No trash or total-disk quota is
  implied. Linux readers can retain blocks after unlink. Late producers can
  recreate old staging; producer cancellation is external.
- Builds use only the digest-pinned SDK/runtime in `plugin-abi-release.json`.
- Releases reuse a successful CI run at the exact tagged commit. Download
  only `plugin-linux-*` and `release-metadata` for publication; keep
  `origin-http-*` receipts and runtime logs as CI evidence. Wildcard downloads
  would mix diagnostic directories into the release asset upload.

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
| Hide fails after expiry | Resolve returns expired without URL; acquire returns conflict until hidden. Static reads may still succeed. |
| Catalog write/rename fails after hiding | Keep catalog repair pending, still remove generation-owned bytes; never restore visibility. |
| Deletion fails/partially succeeds | Retain work and retry idempotently with backoff. |
| Pure read of absent ID/member | `not_found` state |
| Terminal metadata already collected | `not_found`, REST 404; no URL |
| Catalog unlink fails | Keep terminal record and retry; do not delete a replacement generation |
| Managed directory/ancestor is symlink or non-directory at startup | Fail initialization |

## 5. Good / Base / Bad Cases

- Good: acquire once, producer writes and closes files under `stagingDir`,
  commit once, then resolve the entrypoint or a committed member.
- Good recovery: trash disappears while catalog writes keep failing, then metadata
  recovers; restart from the old elapsed ready catalog remains safe.
- Base: concurrent equivalent acquires share one generation and wait for its
  terminal result.
- Good collection: no requests arrive after expiry; bytes and terminal catalog
  disappear, and a later acquire can publish a new generation safely.
- Bad: callers provide member inventories, write outside `stagingDir`, reuse a
  stale generation, or expose staging paths through a read-only resolve.

## 6. Tests Required

- Unit: strict decoding, identity/policy conflicts, concurrent waiter binding,
  atomic visibility, traversal/symlink/special-file rejection, size ceilings,
  parent expiry, cleanup, and restart reconciliation.
- Fault tests: independent deletion/catalog write and rename errors; partial
  deletion and hiding errors; no-traffic retries; new-generation safety; capped
  backoff/fair batches/diagnostics; slow-delete publication and shutdown; restart
  before/after hiding, metadata replacement and physical deletion.
- CI: formatting, vet, tests with race detector, both Linux architectures,
  sidecar validation, and load in the matching runtime digest.
- Runtime: verify REST `202/307/404/410`, static `GET`, valid `206`, invalid
  `416`, `Last-Modified` conditional `304`, and restart preservation.
- Runtime HTTP fixtures borrowing the host listener must set endpoint server to
  ref://:9090 with share_http_server enabled. Plain :9090 creates another listener
  and conflicts with the host's existing port.
- RuleGo Server `v0.37.0` static mappings return `405` for `HEAD`; do not add a
  second HTTP server here to compensate for a host capability gap.
- Lifecycle GC: independently execute baseline target failures and candidate
  passes; disabling GC and removing generation guards must falsify key assertions.
  Use fixed barriers for old-generation collection versus acquire, active catalog
  writes versus residue claim, hide reservations and queued deletion ownership.
- Recovery: kill a real isolated process after quarantine claim and after catalog
  unlink, restart, and assert residue reclamation and new-generation safety. Disk
  fixtures alone do not establish process-crash behavior. Preserve actual Linux
  runtime/race receipts and compare publication latency on the same workload.
- Deterministic manual-cleanup fixtures must join the worker before driving
  batches and explicitly close any cursors reopened after Close. Do not run
  manual cleanupBatch concurrently with the live worker. Runtime receipts must
  distinguish graceful restart from SIGKILL followed by startup and native GET.

## 7. Wrong vs Correct

Wrong: add FFmpeg, YouTube, player-session, or HTTP Range logic to this plugin.

Correct: let any producer write a bounded complete output, publish it through
`resourceOrigin`, and let RuleGo's native static route serve the ready bytes.

Wrong: forget retired work after a catalog failure, or retry recursive deletion
of staging/<id> after that ID has acquired a new generation.

Correct: retain generation-scoped work with independent catalog/deletion deadlines;
remove only trash/<id>-<generation> and staging/<id>/<generation>.

Wrong: infer cleanup completion from a missing map entry, unlink an observed ID
after releasing the mutex, or retain a terminal parent for pending children.

Correct: require completed generation-owned cleanup, verify
`m.records[r.ResourceID] == r`, unlink and delete under the same lock, and treat
an absent parent as unavailable.
