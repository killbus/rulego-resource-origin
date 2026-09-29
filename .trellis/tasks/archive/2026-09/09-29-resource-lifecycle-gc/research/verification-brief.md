# Final narrow verification

Active task: .trellis/tasks/09-29-resource-lifecycle-gc. Authorized implementation.
Main integrated TestCheckScanErrorsFailureOnly after predecessor's patch failures.
Production change in reconcile_gc.go registers scanErrors only immediately before
failures; the original completed && !c.failed guard is retained. First same-root
restart now explicitly closes manual cursors before m2 construction.

Read the task context and these changed sections. Do not read channel history,
old transcripts, or scratch snapshots. No source edits; report concrete blockers
to main. You own only research/verification-review.md and verification-*.txt.
Review main's new TestCheckScanErrorsFailureOnly and restart cursor placement.
Then execute (not merely inspect previous logs):

GOMODCACHE=D:/Repositories/rulego-resource-origin/tmp/gomodcache
GOCACHE=D:/Repositories/rulego-resource-origin/tmp/gocache
GOPATH=D:/Repositories/rulego-resource-origin/tmp/gopath
TEMP and TMP=D:/Repositories/rulego-resource-origin/tmp

go test -count=1 -timeout=60s -run '^(TestCheck|TestIndependent)' -v .
go vet .

Only root package; local ./... includes unrelated ignored generated cgo files.
Capture raw output/exit. Do not rerun a passing command. No crash-test rerun:
predecessor reviewed it and main already ran it. GitHub CI will cover full Linux
unit/race and both pinned-runtime architectures. Finish with a concise review
and evidence. No commits, pushes, deletion or new workers.
