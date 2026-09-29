# Pointer-guard removal mutation evidence (2026-09-30)

## Target

TestCheckGCSupersessionPointerGuard (lifecycle_check_test.go), executed against
a candidate-derived snapshot in research/check-tmp/mut-noguard. The only source
change is the pointer identity guard in collectTerminalLocked (reconcile_gc.go),
matching the earlier main-inspected noguard snapshot. The test file is byte
identical to the workspace candidate test (SHA256 251D5BCE...).

## Mutation diff

See noguard-mutation.diff. One line:

- before: if m.records[r.ResourceID] != r || (r.State != stateFailed && r.State != stateExpired)
- after:  if r.State != stateFailed && r.State != stateExpired { // GUARD REMOVED

## Red run

noguard-mutation-red.txt (raw go test -v output):

    === RUN   TestCheckGCSupersessionPointerGuard
        lifecycle_check_test.go:333: A2: old GC damaged replacement catalog: "" open
        ...catalog/3675079f....json: The system cannot find the file specified.
    --- FAIL: TestCheckGCSupersessionPointerGuard (0.04s)
    FAIL
    FAIL    github.com/killbus/rulego-resource-origin    0.979s

The earlier main-inspected noguard-control.txt in check-negative-noguard ran
only TestCheckScannerBatchBoundAndFairness and TestLifecycleGCFailureThenSupersession,
neither of which reaches the superseded-generation catalog collection path, so it
could not expose the mutation. The actual target test now produces the expected
failure on the same mutation.

## Hashes

- mutated reconcile_gc.go SHA256: CDEE57DF656941A01F0797E94CC8E8C983783986883A647D47DD12ECD95B2D68
- test file SHA256 (identical candidate and mutation): 251D5BCECC9EE43C0242DFDD65788E376650DE736C23276EB67EBD3928777715
- workspace candidate reconcile_gc.go SHA256: 076C4619519E3974719C8F9EBCA70C876F549DF35E893FDEC8145FF3FB5AE90A

## Limits

Windows amd64 local run; not a Linux/race receipt. Scratch snapshot
research/check-tmp/mut-noguard is ignored by research/.gitignore; this file and
the raw red output are the exported evidence.
