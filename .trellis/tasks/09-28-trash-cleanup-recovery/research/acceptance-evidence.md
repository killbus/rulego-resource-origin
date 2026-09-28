# Cleanup recovery acceptance evidence

Recorded 2026-09-28 for the local candidate on fix/trash-cleanup-recovery,
based on 2489c730cddfe36eca043e61c667878d4421ffef (v0.1.1). The file hashes
below identify the tested implementation. After commit, obtain its identity with
git log -1 --format=%H -- origin.go cleanup.go; hosted CI must identify that
candidate commit and supply its own artifact/runtime receipts.
This is local validation evidence, not a release or production incident report.

## Environment and validation

Windows amd64, Go 1.25.1. Race checks use CGO_ENABLED=1 and the existing
MinGW GCC 13.2 at D:/Applications/Scoop/apps/perl/5.40.0.1/c/bin/gcc.exe.
Python is CPython 3.14.0. Docker is unavailable on this host.

- go test ./...: passed, including the final consolidated recovery tests.
- go test -race ./...: passed on the final consolidated tests (4.117 s).
- go test -race -run '^TestFailureAndProductionTimeoutWakeWaiters$' -count=20 ./...:
  passed (2.908 s), immediately before the successful complete race run.
- One intermediate race run failed in TestFailureAndProductionTimeoutWakeWaiters
  during testing.TempDir teardown: Windows reported that catalog was not empty.
  No data race was reported. Two initial follow-up requests did not execute
  because automatic approval timed out. On continued retry approval succeeded:
  all 20 isolated repetitions and the complete race suite passed. The teardown
  failure did not reproduce; its cause is not established. This record is retained
  rather than claiming the Windows failure was fixed. Native Linux CI remains open.
- go vet ./...: passed; production Go code was unchanged by review integration.
- gofmt and git diff --check: passed.
- task.py validate 09-28-trash-cleanup-recovery: passed.
- CI YAML parses and all 14 embedded run steps pass bash -n.
- tests/e2e-origin.py --help: passed after the listener correction. This checks
  syntax and CLI parsing only, not container behavior.
- Native Linux amd64/arm64 builds, SDK sidecars, matching-runtime loading and
  the complete HTTP harness: NOT RUN. They remain mandatory under A7.

## Independent review

Trellis channel trash-cleanup-recovery-0928, read-only cleanup-check worker,
Codex provider. Completion events 515/516 and 611/612; findings in messages
514 and 610. The reviewer examined origin.go, cleanup.go, cleanup_test.go,
node.go, README, the resource-origin contract, CI and the integration harness.
It found no concrete lifecycle, generation ownership, accounting or retry defect.
The reviewer did not independently rerun unit/vet/race checks.

One P1 integration-fixture defect was accepted: server ':9090' would allocate
a second listener on the runtime's occupied port. Main corrected the endpoint
to server 'ref://:9090', matching explicit shared-resource resolution and the
existing indexed-media integration fixture. share_http_server remains enabled.
The correction has source and syntax evidence; successful runtime execution
remains an open CI gate. Main also consolidated overlapping catalog-failure
tests and retained write/rename recovery plus both restart boundaries.

## Acceptance mapping

| Criterion | Evidence | Status |
| --- | --- | --- |
| A1 | research/assessment.md: F1-F5, source/runtime, export values, limits | Passed |
| A2 | TestCleanupRetriesWithoutLiveRecordsOrTraffic; actual worker with controlled clock and failing deletion, then recovery | Passed locally |
| A3 | TestCleanupCatalogFailureRecoveryAndSupersession, TestCleanupDeletionDoesNotWaitForCatalogRecovery, TestCleanupPartialDeletionAndNewGeneration, TestCleanupRenameFailureReservesOnlyItsIdentity, TestCleanupRestartAtRetirementBoundaries, TestCleanupFailedCommitRollbackRetiresOwnedReadyTree | Passed locally |
| A4 | TestCleanupBackoffBatchFairnessAndDiagnostics, TestCleanupSlowDeleteAllowsPublicationAndCloseJoins, TestCleanupCloseCancelsQueuedRetries; failure/recovery event checks in A2 | Passed locally |
| A5 | TestRetainedLimitAndOwnedRoot, TestCommitRejectsUnsafeOrOversizePublication; README and resource-origin contract distinguish ready/staging/trash/catalog | Passed locally |
| A6 | Existing origin/node suite plus restart, pending-timeout/new-staging, failed-acquire and startup-fail-fast fault cases; catalog stays v1 | Passed locally |
| A7 | Local format/vet/unit/race passed; Linux native build, sidecar, load and publication/HTTP checks await CI for the final commit | OPEN |

## Limits and next gate

The tests inject failures at precise operations and demonstrate recovery under
those conditions. They neither identify an observed production incident nor
predict a calendar time when storage will fail. E:/Desktop/sxYw0hmQDtSX.json
remains read-only; no production state was changed.

No trash quota was introduced. Failed hiding can still leave a native static
path readable even though Resolve reports expired without a URL. Close waits
for an already-running filesystem syscall. Retry cannot repair a permanently
unwritable filesystem. Pending staging, catalog growth, late producer output,
and Linux readers retaining unlinked blocks remain outside the ready-byte cap.

Task remains in_progress until A7 has evidence for a concrete final commit.
Local verification is complete for the candidate commit. Push, PR, merge and
release have not been performed. Branch CI is prepared to run on
fix/trash-cleanup-recovery. Record the eventual CI candidate commit,
CI run, per-platform artifact hashes, runtime digest and harness receipts here
before declaring completion.

## Local candidate file identities

SHA-256 values below identify the reviewed and tested local candidate files.

| File | SHA-256 |
| --- | --- |
| origin.go | 8bdd3b8025277251e6ffd07ce1c81c643ed15a22d79242e7c1318566cca129c2 |
| cleanup.go | 451b5138d69e76459ebdce28469c8bbe6342ee92903fa1562208ff45d9c83c83 |
| cleanup_test.go | 44089c8cd885d4a6e0fd65de9360e51dcff2f0d656db2a2745f4697a321c6aa2 |
| node.go | ab1fa2bdc8696e2ac2bdd319cecda27d44882c2064d003536c4d7d128c4fb912 |
| README.md | 1855be1b9d6113a8fe7cff3b79fbf84a5110c40f754dde58da7a020f47505dae |
| .trellis/spec/backend/resource-origin.md | 761b609fd45bb517bce9c51cb2a35ef6cae32d965d6a1294ada0b566d3bc46d6 |
| .github/workflows/ci.yml | eee0a21378c1d535b6dae7bad64c54d08b34c0afc37d2c335a0afd541cfa4415 |
| tests/e2e-origin.py | 837b7dad25cd66bcf42e4174b9b42278c1734c3f15f0905f0cfe98e03dab77dc |
| plugin-abi-release.json | 4054332fd2c9ba70630d556d69c95c0c0bda1958c8f6ded9230980b9d1d6f36b |
