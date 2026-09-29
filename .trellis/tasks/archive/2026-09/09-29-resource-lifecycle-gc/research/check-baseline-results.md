# Independent baseline acceptance evidence

This is pre-implementation acceptance preparation by the channel check worker,
2026-09-29. It is not a review or approval of the candidate. Tests and the
adversarial case matrix were authored before inspecting candidate production
code or implement-worker tests. Only the immutable baseline's production and
existing cleanup tests were read to identify pre-existing APIs/fault hooks.

## Identity and execution

- Baseline: `b22d4b8a08a8a46d3e01b5ced7a154642f710e5c`.
- Windows amd64, Go 1.25.1; default `CGO_ENABLED=0`.
- Snapshot produced by `git archive` of that exact commit, with only
  `lifecycle_independent_test.go` added. No candidate code was copied.
- Reproduction script: `check-run-baseline.py`; it refuses to overwrite an
  existing snapshot. Production sources in the snapshot were not patched.
- Command: `go test -count=1 -v -run '^TestIndependent' ./...`.
- Result: exit 1; package execution 30.125 seconds. All tests compiled and
  executed. Twelve top-level groups / nineteen leaf cases: seven failing
  groups, five passing groups; ten failing leaves, nine passing leaves.
- `go vet ./...` in the same snapshot: exit 0.
- `gofmt -l lifecycle_independent_test.go`: no output.
- The immutable archive hash, test hash and exact environment overrides are
  in `check-baseline-environment.json`; full raw output is retained in
  `check-baseline-unit.log`. No prior evidence was overwritten.

The generated `check-baseline-snapshot/` remains on disk, including build
cache and extracted baseline sources. A guarded removal of this isolated
snapshot was rejected by automatic approval review (`blocked by policy`, no
more specific reason provided). The removal was not retried. This generated
directory is not a source/evidence deliverable and must not be committed.

`TEMP`, `TMP`, and `GOCACHE` pointed inside the isolated snapshot to avoid
user-directory sandbox permissions. Tests did not encounter the previous
`resolve root: Access is denied` failure or a TempDir teardown error. Directory
symlinks were successfully created on this host; those cases did not skip.
Filesystem format was not established by the sandboxed environment probes.
No baseline race run, native Linux HTTP run or process-kill experiment was
performed in this preparation.

## Target failures and preserved safety behavior

All names below are prefixed `TestIndependent`. Assertions inspect operation
results and disk state. Existing fault hooks control barriers and failures;
there are no candidate-specific GC interfaces in this test file.

| Test | Requirement | Baseline result | Exact failing assertion / scope |
| --- | --- | --- | --- |
| TerminalCatalogAndResolveReleased | A1/A6 | FAIL | `A1: completed terminal catalog files remain without a retention advance` (40 distinct failed IDs; logical clock never advances) |
| DeliveredWaiterSurvivesGC | A1/A6 | FAIL | `A1: delivered waiter incorrectly retains terminal catalog`; the pre-GC waiter delivery assertion passed |
| OldDeleteBarrierPreservesReplacement | A2/A4 | PASS | Old staging deletion paused; same-ID replacement committed before release; replacement catalog/bytes survived, old tokens rejected, restart restored ready |
| PendingChildDoesNotRetainParent / parent_gc=false | A6 | PASS | Failed deletion holds terminal parent; child Commit gives parent_unavailable and failed state |
| PendingChildDoesNotRetainParent / parent_gc=true | A1/A6 | FAIL | `A1/A6: pending child prevented terminal parent catalog GC` |
| DeletionProceedsDuringCatalogFailure / write, rename | A1/A2/A3 | FAIL (both) | `A1: repaired terminal catalog was not collected`; prior assertions confirmed bytes were removed while original catalog remained and Resolve stayed expired during the fault |
| NoTrafficRemnantsAndActiveProtection | A3/A4 | FAIL | `A3: no-traffic worker retained unknown/temp/old-generation remnants`; no manager calls or signal after fixture population |
| FailingOldestDoesNotStarveHealthyCleanup | A5 | PASS | One permanently failing oldest generation, 65 healthy generations; all healthy staging cleared, only one same-time attempt on failure, failing catalog retained |
| HideReservationSurvivesRepeatedDiscovery | A4 | PASS | Rename failure preserves ready bytes and makes same-ID Acquire return conflict under remnant pressure; scanner claim is not yet proven |
| RecoveryClaimedRemnantAndCatalogUnlink / claimed, catalog_unlinked | A2/A3/A6 | FAIL (both) | `A2/A3: recovery retained claimed/temp/terminal residue`; valid ready survived and pending token was rejected before failure |
| ManagedDirectoryAnomalyFailsStartup / catalog, staging, ready, trash | A3 | PASS (all four) | Startup rejected managed-directory components replaced with regular files |
| SymlinkLeafAndManagedRootSafety / leaf | A3 | FAIL | `A3: no-traffic leaf symlink was not collected` |
| SymlinkLeafAndManagedRootSafety / managed_directory | A3 | FAIL | `A3: startup accepted symlinked managed directory`, then external sentinel read failed with `The system cannot find the file specified.` |
| CatalogTempPersistBarrier | A4 | PASS | Paused writer's temp survived; resumed Acquire persisted correct pending generation; does not yet establish a scanner-attempt barrier |

The combined recovery failure is not credited as proof of every individual
recovery defect: its condition includes terminal JSON, temp and claimed paths.
Likewise parent GC failure does not isolate a child-reference retention bug in
the baseline, which does not collect any terminal catalogs. These assertions
remain valid regressions for the required final behavior.

## Concrete baseline path-safety counterexample

With the first manager closed, the test renames the owned empty `trash`
directory aside, then creates `trash` as a directory symlink to a second
`testing.TempDir` outside the owned root. That second directory contains only
a sentinel. Baseline startup succeeds and its trash reconciliation removes
the sentinel. This reproduces traversal through an abnormal managed ancestor
on the real Windows filesystem. The target was an isolated test directory,
not a production or user-data directory.

This is a baseline defect and a candidate acceptance requirement. No conclusion
about the candidate's path guard is made until its code is inspected and these
tests are independently executed against it.

## Limits and next gate

The preparation exposes missing baseline behavior; candidate acceptance is
pending. Specific missing barriers, negative controls, scanner fairness,
diagnostics, memory inventory checks and runtime/crash checks are listed in
`check-preparation.md`. The supervisor must send candidate-ready before full
review. No production, other test, spec, status, commit, push, merge or deploy
action was performed by this worker.

The earlier Windows `TestFailureAndProductionTimeoutWakeWaiters` teardown
failure remains unresolved historical evidence; this run neither reproduced
nor explained it. The source-inferred descriptor-pointer race F13 also remains
separate and is not credited to GC evidence.
