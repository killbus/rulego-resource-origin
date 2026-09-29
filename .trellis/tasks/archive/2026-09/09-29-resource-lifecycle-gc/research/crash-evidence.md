# Process-crash evidence

Main integration, 2026-09-29 16:48 UTC, Windows/amd64.

Command: `go test -count=1 -timeout=60s -run '^TestLifecycleCrash' -v .`
with workspace-local GOMODCACHE/GOCACHE/GOPATH/TEMP/TMP. Exact execution
session 36658 completed with exit 0; output in crash-target-final.log.
The helper-only test skips without its environment; both parent tests passed
and launched the real helper executable, waited for a post-mutation barrier,
killed it, and waited for process termination.

Windows cases: quarantine rename before deletion queue registration; terminal
catalog unlink before in-memory removal. Restart reclaims residue and preserves
an unrelated ready payload. Reusing the reclaimed identity and a second restart
preserves the replacement catalog generation and payload.

Earlier fixture versions manually drove cleanup concurrently with a live worker
and intermittently failed their final assertions. A later draft reset the done
channel after joining the worker, which would deadlock Close; that draft was
rejected before final validation. Main removed both patterns and uses ordinary
Close/reopen for replacement persistence. These were fixture errors, not proof
of a production recovery failure. No historical failure logs were erased.

The first main command used malformed Windows path literals and Go rejected its
relative GOPATH before running tests; corrected forward-slash absolute paths
were used for the successful execution above.

The pinned-runtime HTTP fixture additionally executes docker kill --signal KILL,
start, readiness, resolve, and native payload GET after the existing graceful
restart. Final CI 36604788001 at 804d88e passed Linux unit/race and both pinned
runtime architectures. Both downloaded receipts explicitly record process-kill
restart preservation. The instrumented subprocess windows and the runtime
healthy-ready restart are distinct experiments. Process crashes are tested;
power-loss durability is outside scope.
