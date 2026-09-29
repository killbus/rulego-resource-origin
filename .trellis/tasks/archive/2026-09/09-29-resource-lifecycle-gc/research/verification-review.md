# Final narrow verification — independent check worker

Date: 2026-09-30. Scope: reconcile_gc.go failure registration and restart cursor
placement, main's new TestCheckScanErrorsFailureOnly, e2e SIGKILL addition, spec
additions. Read artifacts: check.jsonl manifest (all five files), prd.md,
design.md, implement.md, spec/backend/resource-origin.md.

## Review findings

### scanErrors registration (reconcile_gc.go:109-131, 136-140, 152-156, 187-190, 219-221)

Correct against the contract added to the spec. The deferred guard at :118-126
is unchanged from the original: a turn with any new failure (registered
explicitly at :129, :139, :153, :188, :220 before cleanupFailedLocked emits the
event) records the diagnostic; only a completed pass with no cursor failure
clears it. Registration now precedes cleanupFailedLocked at every site, so the
first event's Pending (cleanup.go:163-169) already counts the failed root.
cleanupFailedLocked increments work.failures before the event, so the failure-
then-clean subtest's failures==32 assertion matches one diagnostic per failed
claim. Backlog clearing on a later entirely clean pass is exercised by the
subtest's final two scans (:214-221 of the test).

One known limitation, not a defect: a Readdirnames error mid-pass registers the
diagnostic and completes the pass through the child/root close path, so c.failed
(set by the deferred guard when failures changed) suppresses the same-pass
EOF clear. That is the intended failure-only semantics.

### Restart cursor placement

expiryLoop closes residue cursors on exit (origin.go, defer at expiryLoop).
TestCheckLateRecreationAndQuarantineRestart closes them again after checkManual
joins the worker (:261) because checkScan reopens cursors during manual scans,
and m2 is closed with an explicit cursor close before the third manager (:275).
lifecycle_crash_test.go closes m/restarted gracefully before reopening the same
root. This matches the spec line: manual fixtures must join the worker and
explicitly close cursors reopened after Close. Checked that double Close and
double closeResidueCursors are safe (nil-check + zeroing struct).

### TestCheckScanErrorsFailureOnly (lifecycle_check_test.go:158-224)

Both subtests fail-first and healthy are deterministic: worker joined before
manual batches, scanNext/scanRoot/work.next reset under mu, first scan leaves a
partial cursor (32-entry batch against 40 entries), and the rename hook rejects
exactly the first 32 claims. The healthy subtest asserts no backlog is
registered on a partial-but-clean pass (:196-199) and cleared after EOF
(:200-203); the failure subtest asserts per-claim failure counting, first-event
pending inclusion, no clear at same-pass EOF (:211-216), and clearing only
after a later fully clean pass. No source edits were made; no defects found.

### Other diff content

- e2e-origin.py: SIGKILL + start + resolve 307 + GET byte check is consistent
  with the existing graceful-restart section and the new receipt entry.
  Syntax verified (python -m py_compile, exit 0).
- Spec additions (:45-47, :141-142) match implemented behavior and the fixture
  rules used by lifecycle_check_test.go.

## Executed verification (this run, not prior logs)

Environment: GOMODCACHE/GOCACHE/GOPATH/TEMP/TMP under
D:/Repositories/rulego-resource-origin/tmp; Windows amd64; Go module
github.com/killbus/rulego-resource-origin.

1. go test -count=1 -timeout=60s -run '^(TestCheck|TestIndependent)' -v .
   Exit 0. PASS; ok github.com/killbus/rulego-resource-origin 3.981s.
   7 TestCheck top-level tests (2 subtest RUN entries) and 12 TestIndependent
   top-level tests (12 subtest RUN entries), all PASS. Counts corrected by main
   against the raw RUN lines. Raw output: research/verification-test.txt
   (exit_code=0 appended).
2. go vet . — Exit 0, no output. Raw: research/verification-vet.txt
   (exit_code=0 appended).

Not rerun per brief: crash tests (predecessor reviewed; main already ran), full
Linux unit/race and dual-architecture CI (GitHub CI scope).

## Blockers for main

None found. Commands passed on first execution; no rerun performed.
