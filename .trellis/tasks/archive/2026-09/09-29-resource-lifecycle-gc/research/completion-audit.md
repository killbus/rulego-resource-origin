# Completion audit

A1–A7 is complete for source commit 804d88e733848564fda07651b698d6904a5b9d45.
Main integrated the work; independent channel workers authored/executed tests.
Final CI: https://github.com/killbus/rulego-resource-origin/actions/runs/36604788001
All four jobs passed. Subsequent archive/journal commits are documentation only.

| Requirement | Evidence and result |
| --- | --- |
| A1 disk/memory GC, no retention | IndependentTerminalCatalogAndResolveReleased creates 40 failed IDs without clock advance; catalogs disappear and all Resolve states become not_found. The absent-record Resolve branch and map deletion were reviewed. GC-disabled candidate fails this target assertion (nogc-mutation.md); candidate passes independently and in CI. |
| A2 failure/restart/generation safety | LifecycleGCFailureThenSupersession and independent write/rename/delete barriers pass. CheckGCSupersessionPointerGuard forces replacement during old deletion; removing the guard destroys the replacement catalog (noguard-mutation.md). Crash tests preserve replacement through a second restart. |
| A3 idle residue/active/path safety | Independent no-traffic, managed-directory, symlink, startup-classification and linked-ancestor cases pass on candidate Windows/Linux. Baseline actually deleted an isolated external sentinel through a managed-directory symlink. CheckLateRecreationAndQuarantineRestart verifies recreated retired staging and quarantine recovery. |
| A4 interleavings and real crashes | Fixed delete/persist/hide barriers and ownership tests pass. Real child processes are killed after quarantine rename and catalog unlink. Both pinned runtime architectures separately pass SIGKILL/start/resolve/native GET preservation. |
| A5 bounds/fairness/diagnostics/publication | CheckScannerBatchBoundAndFairness, ScanFailureDiagnostics, ScanErrorsFailureOnly, LifecycleScanFailureBackoffAcrossPasses, CleanupBackoffBatchFairnessAndDiagnostics and CleanupSlowDeleteAllowsPublicationAndCloseJoins pass. Cross-batch diagnostics falsify premature EOF clearing and phantom backlog mutations. Same-workload performance evidence is exported with limitations. |
| A6 compatibility/no permanent retention | Final Linux formatting/vet/unit/race pass. Both runtime receipts pass REST/static HTTP, parent-child expiry, autonomous expiry, physical trash/catalog removal and restarts. Observed terminal status is 404 on both; transient 410 need not be observed. Parent/waiter tests pass; catalog v1 and rollback limits remain documented. |
| A7 independent proof | Immutable baseline b22d4b8 has 10 target failing leaves and 9 passing leaves, without compilation/environment failure. Independent verify-only worker executed all TestCheck/TestIndependent cases plus vet on final candidate, first-run success. GC-disabled and guard-removal controls fail their target assertions. |

## Evidence identities

- Baseline: b22d4b8a08a8a46d3e01b5ced7a154642f710e5c; check-baseline-results.md.
- Initial candidate: 153ab1d; CI 36595648624 passed before supplemental/crash coverage.
- Final source: 804d88e; CI 36604788001 passed all four jobs; ci-evidence.md and
  ci-final-artifacts contain runtime identities and receipts.
- Independent candidate: verification-test.txt exit 0, 3.981s; verification-vet.txt
  exit 0; review found no blockers. Main corrected only summary test counts.
- Main Windows subprocess cases: crash-target-final.log exit 0, 2.368s.

## Limits and preserved failures

Windows Go 1.25.1 lacked a working local gcc/Docker/WSL setup. An earlier Zig race
attempt produced no successful result. Ignored generated cgo scratch pollutes local
./..., so focused local tests target the root package; clean Linux CI checks ./....

Historical Windows TempDir catalog-not-empty failures remain in raw logs. Later
isolated passes do not establish a universal root cause. Baseline performance
output also contains teardown failure followed by trailing PASS; it is not clean.
Shared-host measurements have high variance/adaptive sample sizes; no reliable
speedup, percentile or latency SLO is claimed (performance-evidence.md). F13 remains
a historical source-inferred descriptor-pointer concern without a dedicated
reproducer; a clean race run does not prove absence of every possible race.

Process-kill evidence does not establish power-loss durability. Single-manager and
exclusive-root assumptions remain. Path validation does not claim protection from
hostile concurrent ancestor replacement. Permanent filesystem failures or continuous
late writes need not converge within bounded time.

Worker patch errors/main integration corrections are preserved in closeout-review.md.
Earlier environment/compile failures are not target regressions. An earlier scratch
deletion was rejected by automatic review; scratch was retained and excluded by
research/.gitignore, with no deletion workaround. Snapshots/caches/payloads are not
deliverables. Subsequent evidence/journal commits do not change tested source.
