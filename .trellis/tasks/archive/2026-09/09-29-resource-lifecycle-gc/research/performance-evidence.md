# Same-workload performance observations

The independent worker used the byte-identical `performance-harness.go.txt` in
two source snapshots. Main exported the original logs and checked every production
Go source file against git: baseline (4 files) matches b22d4b8a08a8a46d3e01b5ced7a154642f710e5c;
candidate (5 files) matches 153ab1d34a5b4ec894b53622adce037af827f0b3 after newline
normalization. Exact snapshot hashes are in performance-source-identities.json.
The historical directory named check-negative-nogc is the immutable baseline,
not a candidate-derived GC-disabled mutation.

Windows amd64, Go 1.25.1, Intel i5-4460; shared host, adaptive iteration counts,
three samples. Background cleanup remains active in both snapshots.
PublishOnly acquires a unique ID, writes a seven-byte payload and commits it.
PublishFailChurn acquires and fails a unique ID; despite its name it does not commit.

| Workload | Baseline ms/op | Candidate ms/op | Interpretation |
| --- | --- | --- | --- |
| PublishOnly | 13.691, 29.568, 49.353 | 11.219, 11.670, 11.865 | Same workload observations; high variance prevents a confident speedup claim |
| Acquire + Fail | 32.557, failed sample, 80.229 | 10.794, 34.654, 15.576 | Baseline teardown failure invalidates a clean comparative conclusion |

The baseline raw log contains a TempDir catalog-not-empty failure followed by a
trailing PASS. The failure is preserved and the entire baseline run is not
classified as clean. These observations do not prove percentiles, a latency SLO,
or final-candidate performance. Deterministic blocking-delete tests and CI race
checks provide the separate evidence for publication-lock behavior.

Raw evidence: performance-baseline-raw.txt and performance-candidate-raw.txt.
