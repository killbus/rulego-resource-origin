# Final integration record

The closeout-check worker independently confirmed the scan diagnostic defects
and restored the original completed && !c.failed guard. It registered diagnostic
work immediately before each of the five actual failure calls. Its source review
confirmed the real subprocess crash hooks, ordinary Close/restart lifecycle, and
replacement generation/catalog/payload assertions (channel event 5181).

Main found the worker's proposed cross-batch regression insufficient: its initial
healthy batch consumed 32 of 40 entries, so the later failure and EOF happened in
one batch. The worker repeatedly failed to apply its follow-up patch and was
stopped; this is not a completed independent verification pass.

Main replaced only that regression with two isolated 40-entry catalog cases,
explicit root selection and retry deadlines. The failing case rejects all first
32 claims, then permits the remaining eight through EOF with no new failure,
asserts retained backlog, and finally completes an entirely clean new pass.
The healthy case asserts no backlog after both partial and complete scans.
Main also moved the first manual cursor closure before the first same-root restart.

Main executed two single-change candidate snapshot controls:

- scan-eof-clear.diff removes only the c.failed guard. The failure_then_clean
  assertion fails at same-pass successful EOF (scan-eof-clear-red.txt, exit 1).
- scan-phantom-backlog.diff registers work unconditionally. The healthy partial
  pass assertion fails (scan-phantom-backlog-red.txt, exit 1).

Each opposite subcase passes, distinguishing the two faults. Snapshots are
ignored scratch; exact diffs and raw outputs are exported. The independently
executed candidate result belongs in verification-review.md. Final Linux/race
and runtime results belong in ci-evidence.md.
