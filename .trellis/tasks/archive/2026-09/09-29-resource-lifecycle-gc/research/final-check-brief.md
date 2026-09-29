# Final independent review

Active task: .trellis/tasks/09-29-resource-lifecycle-gc. Preserve session findings, but this brief supersedes all earlier inbox instructions. Do not repeat unchanged full-suite runs. GitHub CI is the final Linux/race/e2e environment.

1. Fix checkManual explicit closeResidueCursors teardown and before same-root restarts.
2. Fix first scan failure Pending registration before cleanupFailedLocked; assert first event Pending >= 1 and successful recovery clears diagnostics. Current completed-clear diff alone misses first event.
3. Export actual pointer-guard-removed target red raw output and mutation diff/test hash OUTSIDE ignored snapshots. Existing noguard-control.txt was PASS when main inspected.
4. check-negative-nogc is baseline production: label it baseline rerun and add actual candidate-derived GC-disabled mutation proof.
5. Export identical performance harness (as .go.txt) and baseline/candidate outputs outside scratch. /check-perf-*/ is now ignored. Preserve prior Windows failures without claiming experimentally established root causes.
6. Write concise check-results.md with precise evidence and limits. Run only necessary focused verification with exact tracked handles. Silence is not process exit.

Editable: reconcile_gc.go diagnostics, lifecycle_check_test.go, review evidence. Do not edit crash test or e2e: final-ci owns them. Review their final diff once available. No commits/push. Do not delete scratch after approval rejection or substitute another tool for rejected deletion. Finish your turn promptly for main integration.
