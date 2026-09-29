# Narrow final review

Active task: .trellis/tasks/09-29-resource-lifecycle-gc (approved implementation). Main owns commits and GitHub CI. Earlier reviewers accumulated too much context; use saved facts below, not channel history.

Own ONLY reconcile_gc.go, lifecycle_check_test.go, and research/closeout-review.md. Do not read old raw worker logs or repeat all planning/research. PRD/design/implement and resource-origin spec are sufficient context.

## Required fixes

1. Current uncommitted scanResidueBatch registers scanErrors unconditionally before scanning. This correctly counts the first failure but wrongly counts healthy partial scans. Register immediately before each cleanupFailedLocked call instead (a local failure helper is suitable).
2. Restore the original completed && !c.failed guard. c.failed carries failures across 32-entry batches of a single pass, resets on root reopen, and EOF closes the root. The earlier review changed it to completed alone without proving a bug, allowing premature backlog clearing at EOF after an earlier failed batch.
3. Add focused assertions: healthy >32-entry pass creates no scanErrors; first failure Pending >=1; first-batch claim failure persists at later EOF; a subsequent clean full pass clears it. Avoid random/timing-based file order: make every claim fail in first batch, then allow all claims. Drive only root0 explicitly with scanNext and retry deadline reset.
4. checkManual stops the worker then scans manually, reopening cursors. Teardown now closes those cursors, but TestCheckLateRecreationAndQuarantineRestart also needs explicit cursor closure before each same-root reopen.

## Independent final source review

Review lifecycle_crash_test.go and tests/e2e-origin.py. Main fixed crash tests after worker draft errors: no manual cleanupBatch with live worker; no resetting done; Close then second restart verifies replacement generation/catalog/payload. Both killed-subprocess cases passed locally (crash-target-final.log). E2e adds docker SIGKILL/start/resolve/native GET to fixed runtime; main will run Linux CI.

## Verification

Use existing workspace cache paths with FORWARD SLASHES:
GOMODCACHE=D:/Repositories/rulego-resource-origin/tmp/gomodcache
GOCACHE=D:/Repositories/rulego-resource-origin/tmp/gocache
GOPATH=D:/Repositories/rulego-resource-origin/tmp/gopath
TEMP and TMP=D:/Repositories/rulego-resource-origin/tmp

Run go test -count=1 -timeout=60s -run '^TestCheck' -v . and go vet . after fixes. Only the root package: tmp/race-validation contains unrelated generated cgo files, so local ./... is polluted; clean CI handles ./... and race. Track exact execution handles, never start duplicate runs because output is empty. Preserve failures. No full-suite repetition.

Write closeout-review.md with source findings, commands/results, and any remaining issues, then finish. Main is assembling mutation/performance evidence separately; do not duplicate that work. No commits/push or scratch deletion.
