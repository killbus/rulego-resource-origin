# Independent acceptance preparation

Prepared by the channel check worker on 2026-09-29, before reading candidate
production code or implement-worker tests. The baseline is
`b22d4b8a08a8a46d3e01b5ced7a154642f710e5c`. The oracle is PRD/design v3,
the current resource-origin contract, and manifest errata. Historical failures
in the previous task and `baseline.md` remain unchanged.

## Cases fixed before candidate inspection

| Case | Contract / deterministic schedule | Observable assertion |
| --- | --- | --- |
| C01 | A1/A6: many distinct failed and expired IDs; finish payload work; keep logical time fixed after retirement | catalog JSON absent and Resolve not_found without a retention advance; delivered waiter retains terminal result |
| C02 | A2/A4: stop old generation deletion at a barrier, acquire and commit replacement, then release old cleanup | replacement catalog bytes and ready bytes survive; stale Commit/Fail rejected; restart preserves replacement |
| C03 | A1/A6: parent expires while child remains pending; compare Commit before and after parent GC | parent GC completes despite child; child returns parent_unavailable and terminal failure in both schedules |
| C04 | A2/A3: fail catalog persistence after hiding, permit byte deletion, then recover persistence | payload disappears while metadata repair is pending; terminal record is retained until responsibility completes, then GC |
| C05 | A3/A4: insert old staging, unknown ready/trash/catalog files and directories, and .record-* after startup; cease calls | worker clears leftovers; active staging/ready and their catalog bytes remain unchanged |
| C06 | A3: leaf links to external sentinels; managed directory or ancestor replaced by link | leaf link removed without changing target; abnormal managed path stops cleanup (startup fails) |
| C07 | A2/A4: fixture disk state after claim into trash and after catalog unlink; restart with unrelated valid ready and abandoned pending | trash rediscovers/removes; deleted record stays absent; ready survives; abandoned pending loses lease |
| C08 | A5: >32 failures/healthy work; oldest work permanently fails; fixed clock and counted attempts | healthy work progresses; backoff prevents repeated same-time attempts; batch bound and diagnostic backlog |
| C09 | A4: block generation-owned RemoveAll; scanner observes that path; also hide reservation with records absent | scanner cannot steal inflight work or hide-owned ready path; publication remains possible during slow delete |
| C10 | A2/A4: observe old GC candidate, insert new Acquire before action, then run old action | pointer recheck prevents unlink of current catalog; isolated removed-guard control must fail |
| C11 | A3/A4: stop persist before temp-file rename; schedule scanner; resume persist | scanner cannot claim live .record-*; catalog remains valid; abandoned temp later collected |
| C12 | A4: observe orphan, publish matching ID before claim; recreate old source after isolation | claim revalidates live publication; late source is rediscovered; isolated trash survives process interruption |

## Test structure and evidence rules

- Preparation writes only `lifecycle_independent_test.go` and task research
  `check-*` artifacts. Baseline extraction is an isolated, disposable snapshot
  beneath `research/check-baseline-*`; no shared production/test modifications.
- Baseline-compatible tests use existing manager operation results and files as
  their behavioral oracle. Existing fault hooks may control the schedule, but
  assertions do not depend on a newly introduced GC interface.
- Fixed barriers establish ordering. Polling with a deadline is only a progress
  watchdog, never proof that a race window occurred. No artificial retention
  clock advance is permitted in C01.
- Candidate-specific scanner/GC barriers and negative controls are deferred
  until the main session declares the candidate ready. They will be executed
  in isolated snapshots, never by mutating shared production code.
- Windows filesystem permissions, CGO/compiler availability, compilation and
  fixture teardown errors are separate from target assertion failures.
- Recovery disk fixtures do not constitute process-kill or power-loss evidence.
  Native Linux HTTP, real process interruption, symlinks, and latency comparison
  require separately recorded execution.

## Preparation execution

Completed baseline preparation. See `check-baseline-results.md`, raw
`check-baseline-unit.log`, and `check-baseline-environment.json`. No candidate
implementation or implement-worker tests have been inspected.

The independently authored file contains 12 top-level tests (19 leaf cases).
The baseline compiled and executed: 7 top-level groups failed on contract
assertions and 5 passed (10 failing / 9 passing leaf cases). Baseline vet and
gofmt passed. This is preparation evidence, not candidate acceptance.

## Candidate-ready follow-up (required, not yet executed)

- Read the candidate diff only after main's candidate-ready message.
- Execute these same tests, then inspect in-memory record release, waiters,
  scheduling, cleanup responsibility completion, path checks and diagnostics.
- Add scanner observation/claim barriers for C09/C11/C12. The preparation
  persist test fixes the writer pause but does not prove a scanner attempted
  claim while paused. The hide test protects an existing record; the missing
  record plus hide reservation case remains to be forced.
- The deletion barrier in C02 proves old work completion after replacement,
  but candidate GC selection/action and scanner inflight ownership still need
  explicit scheduling barriers and removed-guard negative controls.
- Use an isolated candidate copy with GC disabled for progress assertions and
  another with the current-generation guard removed for safety assertions.
  Preserve failing target assertions for both controls.
- Exercise >32 scanner candidates, rename failures on one candidate, unknown
  valid-ID ready/catalog leaves, and diagnostic oldest-age/backlog/rate limits.
- Test actual process interruption at claim/unlink boundaries and matching
  Linux runtime HTTP. Existing preparation restart cases model disk states.
- Run candidate full unit/vet/race checks and the agreed performance comparison.
  Historical Windows teardown failure and source-inferred F13 remain separate.
