# Execution plan — local validation in progress

## Planning handoff

- [x] Record source and export evidence in research/assessment.md.
- [x] Write converged PRD, proposed design and real worker context manifests.
- [x] Obtain approval of this repair scope before task.py start.
- [x] Create fix/trash-cleanup-recovery from the reviewed main baseline; confirm
  existing dirty task artifacts are the only local changes before switching.

## Implementation sequence

1. Read the resource-origin contract and this task's evidence. Reconfirm baseline
   behavior and use deterministic fault injection to expose F1-F3. Do not mutate
   the production export or real resource directories to reproduce the failures.
2. Resolve transaction ordering for expiry persistence and same-ID reacquisition.
   Record the chosen recovery invariant in design.md before implementing it.
3. Add generation-owned runtime cleanup work, bounded retry/backoff, fair batching
   and stop integration. Keep healthy expiry deletion immediate and avoid holding
   the publication mutex during slow filesystem deletion.
4. Add sanitized, rate-bounded cleanup diagnostics and recovery reporting through
   the existing host logger. Keep ready-byte admission semantics unchanged.
5. Cover deletion and catalog faults, partial deletion, no-traffic/no-live-record
   retry, permanent failure, new generations, restart boundaries and shutdown.
   Update README and .trellis/spec/backend/resource-origin.md in the same change.
6. Dispatch an independent channel check worker with check.jsonl and all planning
   artifacts. Correct findings and run the applicable verification gates.
7. Record exact commit, environment, checks, artifact identities and limitations in
   research/acceptance-evidence.md before task completion. Do not mark unrun CI or
   runtime integration as passed.

## Worker and validation scope

Use the repository's trellis channel implement/check workflow. Workers may edit
origin.go, origin_test.go, node.go/node_test.go only if logger/config wiring is
needed, README, the resource-origin spec, and narrowly necessary test/CI fixtures.
Changes to schemas, catalog version or capacity semantics require design review.
No production access, arbitrary cleanup commands, unrelated task archival or
hosted repository mutation is part of the worker brief.

Checks after implementation:

- gofmt on changed Go files, go vet ./..., go test ./....
- go test -race ./... on a supported toolchain/runner.
- Deterministic regression cases for every row of design.md's failure matrix.
- Existing lifecycle/strict-input/limits/parent/waiter/path tests.
- Owning CI: metadata, both Linux architecture builds, pinned ABI sidecars and
  matching-runtime smoke checks. Run branch CI before the final merge where possible.
- Applicable host publication/static mapping integration: expired resources are
  unavailable, healthy GET/206/304 behavior and restart preservation remain valid.
- task.py validate 09-28-trash-cleanup-recovery and git diff --check.

## Completion and rollback

Complete only when A1-A7 have authoritative evidence. Keep failed experiments and
unavailable runtime checks explicit. Integrate accepted fixes and documentation in
one coherent release candidate; never reuse an existing release version. Archive
and journal only after implementation review/commit. Rollback must preserve valid
ready media and owned-root/catalog readability; no recursive production deletion.

## Execution progress — 2026-09-28

- User approval received; task started by main session.
- Branch: fix/trash-cleanup-recovery; baseline: 2489c730cddfe36eca043e61c667878d4421ffef.
- Transaction ordering recorded in design.md before implementation.
- Local implementation and deterministic fault tests complete. Independent review found one fixture listener conflict, corrected to borrow ref://:9090. Final verification is recorded in research/acceptance-evidence.md; Linux CI/runtime gates remain open.

Main integrated the worker recovery tests, consolidated overlapping catalog
failure coverage, and extended the restart matrix to catalog rename failure.
Main owns CI, documentation integration and the disposable runtime harness. Local verification and independent review are recorded separately
in research/acceptance-evidence.md; Linux CI/ABI/runtime gates remain required.
