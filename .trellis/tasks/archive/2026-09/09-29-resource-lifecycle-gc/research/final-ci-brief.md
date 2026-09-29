# Final crash verification

Active task: .trellis/tasks/09-29-resource-lifecycle-gc. This brief supersedes earlier inbox instructions. You own lifecycle_crash_test.go (created by your original session), tests/e2e-origin.py, and crash evidence.

1. Replace BOTH final retireCleanup/cleanupBatch tails with Close then second restart asserting replacement ready generation/catalog/payload survives. Do not manually invoke cleanupBatch concurrently with a live worker. Remove DEBUG instrumentation.
2. Fix crashOutput.Write return count when buffer is already full: discard while returning original len(payload), nil.
3. E2e KILL/start change is already present from your resumed session: retain it and verify accurate receipt. No further fixture expansion needed.
4. Run one bounded targeted crash test with exact tracked handle and record full stdout/exit in research/crash-evidence.md (or linked raw log). If Windows run blocks, report precise condition; main runs Linux CI. Never start duplicate tests merely because output is empty.
5. Do not delete scratch after any approval rejection, including using Python instead of rejected shell removal. Retain generated pycache locally; main handles ignore entries.

No commits/push, no production edits. Finish your turn after these concrete changes and evidence so main can integrate and run GitHub CI.
