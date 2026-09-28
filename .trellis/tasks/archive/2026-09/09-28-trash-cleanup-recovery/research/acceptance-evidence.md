# Cleanup recovery acceptance evidence

Recorded 2026-09-28 for implementation commit a1d33cec0c69b221e1ae6aaff25ff75c3912bce3
on fix/trash-cleanup-recovery, based on 2489c730cddfe36eca043e61c667878d4421ffef
(v0.1.1). This combines local validation, independent review, and successful
hosted CI with downloaded artifact verification. Subsequent task/archive/journal
commits change documentation only. This is not a release or production incident report.

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
  rather than claiming the Windows failure was fixed. Native Linux CI subsequently passed as recorded below.
- go vet ./...: passed; production Go code was unchanged by review integration.
- gofmt and git diff --check: passed.
- task.py validate 09-28-trash-cleanup-recovery: passed.
- CI YAML parses and all 14 embedded run steps pass bash -n.
- tests/e2e-origin.py --help: passed after the listener correction. This checks
  syntax and CLI parsing only, not container behavior.
- Native Linux amd64/arm64 builds, SDK sidecars, matching-runtime loading and
  the complete HTTP harness: passed in CI run 36431435634 for a1d33ce.
- Linux format/vet/unit/race and release metadata checks: passed in the same run.

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
The corrected shared listener passed runtime integration on both Linux architectures. Main also consolidated overlapping catalog-failure
tests and retained write/rename recovery plus both restart boundaries.

## Acceptance mapping

| Criterion | Evidence | Status |
| --- | --- | --- |
| A1 | research/assessment.md: F1-F5, source/runtime, export values, limits | Passed |
| A2 | TestCleanupRetriesWithoutLiveRecordsOrTraffic; actual worker with controlled clock and failing deletion, then recovery | Passed locally and in Linux CI |
| A3 | TestCleanupCatalogFailureRecoveryAndSupersession, TestCleanupDeletionDoesNotWaitForCatalogRecovery, TestCleanupPartialDeletionAndNewGeneration, TestCleanupRenameFailureReservesOnlyItsIdentity, TestCleanupRestartAtRetirementBoundaries, TestCleanupFailedCommitRollbackRetiresOwnedReadyTree | Passed locally and in Linux CI |
| A4 | TestCleanupBackoffBatchFairnessAndDiagnostics, TestCleanupSlowDeleteAllowsPublicationAndCloseJoins, TestCleanupCloseCancelsQueuedRetries; failure/recovery event checks in A2 | Passed locally and in Linux CI |
| A5 | TestRetainedLimitAndOwnedRoot, TestCommitRejectsUnsafeOrOversizePublication; README and resource-origin contract distinguish ready/staging/trash/catalog | Passed locally and in Linux CI |
| A6 | Existing origin/node suite plus restart, pending-timeout/new-staging, failed-acquire and startup-fail-fast fault cases; catalog stays v1 | Passed locally and in Linux CI |
| A7 | Local format/vet/unit/race plus CI 36431435634: both native builds, sidecars, matching-runtime load and publication/HTTP; downloaded bytes and receipts verified | Passed |

## Limits and delivery status

The tests inject failures at precise operations and demonstrate recovery under
those conditions. They neither identify an observed production incident nor
predict a calendar time when storage will fail. E:/Desktop/sxYw0hmQDtSX.json
remains read-only; no production state was changed.

No trash quota was introduced. Failed hiding can still leave a native static
path readable even though Resolve reports expired without a URL. Close waits
for an already-running filesystem syscall. Retry cannot repair a permanently
unwritable filesystem. Pending staging, catalog growth, late producer output,
and Linux readers retaining unlinked blocks remain outside the ready-byte cap.

A1-A7 have passed for implementation commit a1d33ce. The user explicitly
authorized pushing fix/trash-cleanup-recovery and following Linux dual-architecture
CI. Push succeeded and the first candidate run passed. PR, merge, release and
production operations remain outside that authorization and were not performed.
The Trellis implementation task is ready for archival; delivery to main and a
new release are separate steps. Artifact names still contain the existing v0.1.1
metadata; these are branch validation artifacts, not a replacement v0.1.1 release.

## Hosted CI and downloaded artifacts

- Run: [36431435634](https://github.com/killbus/rulego-resource-origin/actions/runs/36431435634),
  head a1d33cec0c69b221e1ae6aaff25ff75c3912bce3, conclusion success.
- All four jobs passed: test, release-metadata, native amd64 build, native arm64 build.
  Completed 2026-09-28 13:52:06 UTC.
- Downloaded origin-http-amd64/arm64, plugin-linux-amd64/arm64 and release-metadata.
  Recomputed SHA-256 from each downloaded .so; checked .sha256, ABI sidecar and
  runtime receipt equality; checked platform, pinned ABI/lock and runtime digest.
  Exact receipts and sidecars are preserved in ci-artifacts.json alongside this file.
- Both receipts confirm REST 202/307/404/410, static GET/206/416/304, parent-child
  expiry, autonomous expiry, physical trash removal and restart preservation.
  Downloaded logs show initial startup and successful restart with the fixture chain.
- Filesystem fault injection runs in the Go suites; the runtime harness checks
  healthy publication and expiry. It does not simulate a broken production disk.
- Artifact download first lacked credentials in the sandbox (HTTP 401). The host
  credential request encountered one automatic approval rate-limit failure; retry
  was approved and the download completed. No command bypassed approval.

| Platform | Plugin SHA-256 | Size (bytes) |
| --- | --- | --- |
| linux/amd64 | ac34a24c066e999ffb6a1dccc6ca370e34ab13cfc39b9bd0b0aede6675e4824e | 13185928 |
| linux/arm64 | 9f66e658df4bc518f71683b98db1c1d730f406b8ddb7086a65fb2d9cab572db7 | 13084740 |

Runtime: ghcr.io/killbus/rulego-server@sha256:8594e773b9d0cf2afa1fd8af0744b9eea5a500006e8847c149ed36cc1fcb559a.
ABI and SDK release identities are preserved in ci-artifacts.json.

## Implementation file identities

SHA-256 values below identify the reviewed implementation files tested locally and in CI.

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
