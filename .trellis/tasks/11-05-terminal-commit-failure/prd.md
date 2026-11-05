# Terminal commit failures release pending generations

## Goal

Make every unrecoverable `resourceOrigin` commit outcome terminal. A failed
commit must retire the current pending generation, notify equivalent waiters,
and allow a later acquire of the same deterministic resource ID to create a
fresh generation instead of leaving callers blocked until the production
timeout sweep.

## Background

Production evidence from the HLS chain shows a producer successfully wrote
3,909,272 bytes, but `commit` returned `storage_limit`. The generation remained
pending. Five equivalent requests then waited on the same resource ID and ended
with `wait_timeout: context canceled` only when the client canceled. The stale
generation was not swept until `publishBy`, five minutes after acquisition.

The defect is in the publication lifecycle, not in the producer or HLS client.
`commit` has enough information to know that the current generation cannot
become ready, but it currently returns the error without publishing a terminal
waiter outcome.

## Requirements

- **R1. Terminal commit outcome.** After `Commit` has found a valid current
  pending generation, an unrecoverable commit error transitions that generation
  to `failed` before the call returns.
- **R2. Waiter notification.** Setting the failed state must notify the current
  waiter with the existing terminal descriptor contract:
  `state=failed` and the commit failure kind.
- **R3. Original caller error.** The caller that received the commit error keeps
  the original error class. A failure to schedule cleanup must not turn a
  capacity or invalid-publication error into a successful commit.
- **R4. Fresh generation.** After the failed generation is retired, an equivalent
  acquire can create a new generation with a different generation token. Tokens
  are never reused.
- **R5. Preserved behavior.** Successful commit, producer-initiated `fail`,
  parent validation, ready expiry, cleanup, restart recovery, static serving,
  and existing failed-waiter behavior remain unchanged.
- **R6. Release identity.** The behavior change is released as plugin version
  `0.2.1`, with version and compatibility metadata updated together.

## Acceptance Criteria

- **AC1 -> R1/R2.** A regression test fills the retained-byte budget, starts a
  second production, commits the second generation, and proves the second
  generation becomes failed and its equivalent waiter receives a terminal
  failed result immediately.
- **AC2 -> R1/R2.** The waiter result includes `failureKind=storage_limit` and
  does not observe a context timeout.
- **AC3 -> R3.** The commit caller still receives an error recognized as
  `storage_limit` after terminalization.
- **AC4 -> R4.** After the failed generation is collected, an acquire of the same
  key returns a new pending generation with a different token.
- **AC5 -> R5.** Existing unit, race, cleanup, restart, and runtime suites pass.
- **AC6 -> R5.** A focused test covers at least `invalid_publication` and one
  publish-path failure in addition to `storage_limit`, proving the terminal path
  is not special-cased only for capacity.
- **AC7 -> R6.** `VERSION`, `compatibility.json`, and the component `Version()`
  agree on `0.2.1`; CI passes.

## Out of Scope

- HLS chain configuration, TTL policy, viewer sessions, or media quality.
- Retained-byte limits, eviction policy, disk provisioning, or transcoder
  scheduling.
- Idempotent `fail` after a generation has already been collected.
- A new `commit --Failure--> fail` graph convention.
- Commits, releases, or deployment by the implementation sub-agent.

## Risks

- Failing on publish-path errors changes the retry unit from the current
  generation to a fresh generation. This is intentional: a new acquire is the
  documented recovery path and avoids indefinite pending state.
- Cleanup can itself fail. The failed state and waiter notification must still be
  published, with cleanup remaining retryable.
- Terminal records are collected without retention. A later `fail` operation may
  still observe `stale_generation` or`not_found`; this task does not add a
  tombstone or promise post-collection idempotency.
