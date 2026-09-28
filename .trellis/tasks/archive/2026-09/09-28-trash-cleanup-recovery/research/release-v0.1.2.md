# v0.1.2 release preparation

The user authorized release after merging PR #1. The release preparation updates
VERSION, compatibility.json and node metadata from 0.1.1 to 0.1.2. Plugin ABI and
runtime pins are unchanged. The release workflow now selects plugin-linux-* and
release-metadata explicitly: origin-http-* diagnostic directories are CI evidence,
not publishable assets.

## Local validation on Windows, 2026-09-28

Version consistency, gofmt and git diff --check passed. Full unit runs failed in
testing.TempDir teardown with Windows reporting catalog as not empty. Failures
occurred in TestCleanupRestartAtRetirementBoundaries/after_hide and
TestCleanupPartialDeletionAndNewGeneration. Twenty repetitions of the restart
test had two teardown failures (after_hide and partial_delete). Do not count these
runs as passed. The earlier implementation evidence records a similar failure.

An independent Go 1.25.1 program reproduced the same error without importing
plugin code or launching goroutines: each iteration created a temporary catalog,
wrote/synced/closed eight temporary files, renamed them over two JSON records,
and called os.RemoveAll. Nine of 100 iterations returned directory-not-empty;
os.ReadDir immediately afterward returned an empty list without error each time.
This establishes that this local teardown symptom can occur independently of
the plugin. It does not identify the underlying Windows/toolchain cause.

Production catalog writes close their file before rename; manual test managers
join the worker before test operations, and Close joins the worker. No production
change or test suppression was introduced for the Windows teardown symptom.

## Publication gate

The deliverables are Linux amd64/arm64 plugins. Publish only after the exact
versioned commit passes all CI jobs: format, vet, unit/race, both native builds,
pinned ABI sidecars, runtime loading and HTTP lifecycle checks. Check downloaded
plugin hashes against checksum sidecars, ABI sidecars and runtime receipts.
The release workflow verifies the CI SHA equals the tagged SHA and publishes
those tested artifacts without rebuilding. The GitHub release records the final
CI evidence links; no production deployment is authorized by this release.
