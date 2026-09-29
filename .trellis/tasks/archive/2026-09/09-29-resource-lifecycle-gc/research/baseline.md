# Baseline evidence and limitations

Inspected 2026-09-29. Origin HEAD b22d4b8 includes 098d652 merge of trash recovery. Earlier archived statements about no merge describe 2026-09-28, not current repository state. Deployed plugin identity remains unknown.

Source anchors:
- origin.go:535 failLocked; :550 expireLocked; :575 persistLocked; :606 reconcile; :720 expiryLoop; :779 sweepLocked (verify line numbers against pinned HEAD).
- cleanup.go:98 prepareCleanupLocked persists terminal state; :244 removes cleanup work, not catalog/records.
- indexed-media source.go:101 operationContext connects parent and shared owner cancellation; :335 contextProblem distinguishes deadline timeout from canceled. getBundle coalesces inspection using first caller context; possible cancellation propagation is not a reproduced trash regression.

Historical evidence: .trellis/tasks/archive/2026-09/09-28-trash-cleanup-recovery/research/acceptance-evidence.md. Includes successful Linux dual-architecture CI and an unresolved Windows TempDir teardown failure. Earlier independent reviewer did not rerun suites; this task requires reviewer-executed evidence.

Baseline go test ./... on 2026-09-29: sandbox run failed at manager initialization with resolve root: Access is denied across origin/cleanup tests, plus Go cache trim access denied. This is environment failure, not an expected behavioral red test. Approved unsandboxed rerun passed: ok github.com/killbus/rulego-resource-origin 2.923s (exit 0). No race/vet or new regression tests have been executed in this planning turn. No new failing regression has yet been established. Requirements for metadata retention, pressure policy and playback continuation must be approved before inventing expected failing assertions.

Evidence ledger format for each case: requirement; baseline SHA/environment; fixture/input; observable expectation and its authority; baseline result/failure; candidate result; independent reviewer command/result; limitations. Preserve raw failures. A mocked hook result is not proof of real disk-full, reader lifetime, crash durability or production deployment.

