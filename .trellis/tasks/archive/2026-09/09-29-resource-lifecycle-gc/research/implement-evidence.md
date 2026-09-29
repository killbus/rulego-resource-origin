# Implement worker evidence — 2026-09-29

Baseline: b22d4b8a08a8a46d3e01b5ced7a154642f710e5c. Initial tracked workspace clean; task directory untracked. Windows/amd64, Go 1.25.1, CGO_ENABLED=0. No deployment performed. Historical evidence files unchanged.

## Environment and baseline

Default `go test` failed writing user caches (implement-baseline.txt). Setting GOCACHE to a writable temporary location compiled, but default test TEMP roots failed existing EvalSymlinks initialization with Access is denied (implement-baseline-local-cache.txt). These are environment failures, not red regression evidence.

Working recipe: set GOCACHE to an accessible temporary build directory; set both TEMP and TMP to the absolute workspace path `.trellis/tasks/09-29-resource-lifecycle-gc/research/test-tmp` (create first). Run Go from repository root.

Created an isolated baseline-run directory from `git show b22d4b8:<file>` for tracked root Go files, go.mod and go.sum. Added only the two baseline-compatible tests preserved verbatim in implement-baseline-tests.go.txt. Under the same working environment, `go test -count=1 -run TestLifecycle ./...` produced both target assertion failures (implement-baseline-target.txt):

- A1: terminal record retained after cleanup (4.03s).
- A3: idle managed residue retained (6.03s).

This baseline comparison was executed after initial candidate editing, using untouched baseline source in the isolated directory. Initial pre-edit attempts were blocked by environment.

## Candidate implementation

- Terminal GC is the last phase of cleanupWork. Completed payload removal and persistence attest responsibility. Pointer identity, terminal state, waiter delivery and unlink share the publication mutex. Failed unlink retains work with existing capped backoff; supersession cannot delete a new catalog.
- Startup completes existing synchronous recovery, then collects terminal catalog with explicit completed-work attestation; failure remains fail-fast.
- Periodic round-robin scans use four bounded directory cursors, at most 32 observations per turn, one second scheduling opportunities, one staging child handle per cursor. Root scan failures retain capped backoff and queue-age diagnostics.
- Residue is renamed under the mutex into a unique MkdirTemp trash container; existing cleanup queue deletes it outside the mutex. Queued paths, pending staging, pending/ready publication and hide reservations are protected.
- Managed directories and ancestor components are checked with Lstat; staging parent components are checked before generation deletion. Unknown catalog names, .record-* and leaf links are cleaned without following link targets. External adversarial concurrent path replacement is not claimed to be prevented.
- Existing absolute TTL, parent validation, catalog v1 and native HTTP implementation retained. README documents terminal not_found/404, single-manager assumption, recovery and rollback.

## Executed validation

- `go test -count=1 -run '^TestLifecycle' ./...`: PASS, 4.011s (implement-target-green.txt). Includes terminal collection, idle discovery, unlink failure/backoff followed by new-generation supersession and stale-token rejection.
- `go vet ./...`: PASS, exit 0 (empty implement-vet.txt).
- `go test -race ./...`: unavailable: `-race requires cgo; enable cgo by setting CGO_ENABLED=1`.
- `git diff --check`: PASS.
- `python .trellis/scripts/task.py validate 09-29-resource-lifecycle-gc`: PASS (4 implement, 5 check manifest entries).
- Full `go test -count=1 ./...` executed three times after substantive corrections; raw logs implement-workspace-test*.txt retained. First run exposed old startup cleanup-count assertions and a waiter-fixture Windows teardown error; second exposed the changed live-expiry assertion and independent persist-barrier teardown; third had no target behavior assertion failures but FAILED on Windows TempDir cleanup for TestCleanupRestartAtRetirementBoundaries/partial_delete and TestCleanupSlowDeleteAllowsPublicationAndCloseJoins. Do not report the full suite as passing.
- Earlier candidate default-TEMP failures retained in implement-test.txt. A concurrent incomplete independent test briefly caused an unused-import compile failure; that is not a behavioral defect. Independent test source is owned by the check worker, not modified here.

## Teardown investigation and remaining evidence gaps

Close joins the worker and closes scanner cursors before done is closed. Examining the failed fixtures after process exit found only empty catalog directories, no catalog files; full paths are recorded in implement-teardown-residue.txt. This does not establish a cause or prove absence of an OS handle issue. It also does not resolve the historical TestFailureAndProductionTimeoutWakeWaiters evidence. No sleeps/retries were added to hide teardown failures.

Independent tests were included in full-suite execution; independent acceptance remains the check worker's responsibility. Linux native HTTP, real crash/kill boundaries, open readers, permission failures and publication latency/throughput comparison remain unexecuted here. Docker was not found; `wsl --list --quiet` returned WSL installation/help rather than an installed runtime. Existing `.github/workflows/ci.yml` has Linux amd64/arm64 pinned SDK build and runtime HTTP infrastructure; no workflow was dispatched. Runtime identity remains the digest in plugin-abi-release.json.

Scratch cleanup was rejected by automatic command policy, including after explicit absolute-path verification. `baseline-run/` and `test-tmp/` remain generated local scratch (the latter includes a build cache); do not commit them. Baseline test snapshot and raw evidence are outside those directories. Main session can remove scratch using its permitted environment.

## HTTP fixture follow-up

Updated tests/e2e-origin.py to accept expired/410 or already-collected not_found/404 after autonomous static expiry, require convergence to 404, verify terminal catalog removal, and reject URL/Location leakage in both terminal responses. Receipts list observed terminal status codes rather than claiming a transient 410 was necessarily exercised. Static GET, range, conditional requests, shared host listener and pinned runtime identity are unchanged. Python AST syntax validation passed; Docker/runtime execution remains unavailable. Main additionally confirmed no gcc on PATH and WSL install-help exit 1. No Linux runtime result is inferred from syntax validation.

## Integration boundary review and final candidate run

Addressed main's four boundary findings: validate existing ancestors before root creation and all managed directories before creating any; Lstat recognized catalog files before reading (reject links/special files); classify unknown catalog names and directories as owned residue while malformed real records remain fail-fast; preserve scan failure counters across failed cursor passes instead of resetting at EOF.

Added lifecycle_boundary_test.go. Isolated boundary-run executed startup catalog classification, linked ancestor creation prevention, and scan backoff across passes: PASS (2.069s), including actual Windows symlinks with no skipped cases. Raw output: implement-boundary-tests.txt. Added a further case ensuring a linked trash directory prevents creation of missing catalog/staging/ready directories. This passed in the subsequent full suite. The isolated boundary-run source copy remains scratch and must not be committed.

Channel message seq 871 confirms the check worker completed preparation. After reading that confirmation, ran the latest shared candidate including lifecycle_independent_test.go (untouched):

- `go test -count=1 ./...`: PASS, 9.249s, exit 0 (implement-workspace-test-4.txt).
- `go vet ./...`: PASS, exit 0 (implement-vet-final.txt).
- `go test -race ./...`: unavailable, exit 2, requires CGO (implement-race-final.txt); no gcc available.
- `git diff --check`: PASS.
- `python .trellis/scripts/task.py validate 09-29-resource-lifecycle-gc`: PASS.

The final passing run follows substantive boundary changes; it does not resolve or erase earlier intermittent Windows teardown failures. Earlier full-suite failures, isolated baseline target failures, and environment failures remain preserved. Independent negative controls, Linux runtime/crash evidence, and comparative latency/throughput measurements remain acceptance gaps; no such results are inferred from local unit tests.

## Process-crash e2e extension (post 153ab1d CI dispatch)

Extended tests/e2e-origin.py on top of the already-committed HTTP fixture follow-up. After the unchanged graceful docker restart cycle, the scenario now issues docker kill --signal KILL, polls docker inspect until State.Running is false (10 s deadline), then docker start, wait_ready, resolve of the restart-preserved resource expecting 307 with the identical URL, and a native static GET asserting the exact original payload bytes on the same persisted root. This exercises startup reconcile after a process crash that skips every shutdown path; it is process-crash evidence only and does not claim power-loss durability. Receipt checks gains the distinct label 'process-kill restart preservation'; the existing 'restart preservation' label still refers to the graceful cycle.

Verification on this host: python -m py_compile passed and git diff --check is clean. Docker remains unavailable locally, so runtime execution is deferred to the pinned-runtime CI job, which already invokes this script for both Linux architectures. No workflow or release metadata files were changed. This extension supplies CI process-crash evidence in addition to the deterministic unit crash windows in lifecycle_crash_test.go; it does not by itself satisfy the kill-after-quarantine and kill-after-unlink recovery cases, which remain check-worker scope.
