# Implementation plan

1. Bootstrap the minimum Go plugin from the released Plugin ABI SDK contract:
   module, registry, node, README, license, CI, and release workflow only.
2. Implement one stdlib-backed manager owned through RuleGo `SharedNode` for
   identity, state transitions, waiters, atomic catalog writes, commit
   validation, expiry, and restart reconciliation.
3. Wrap the manager in strict `resourceOrigin` acquire/commit/fail/resolve
   decoding, RuleGo relations, and one REST response projector.
4. Add focused tests for concurrency, stale generation, traversal/symlink,
   size limits, parent expiry, atomic visibility, restart, and safe cleanup.
5. Add one generic producer/static-mapping example and ABI/runtime smoke test.
6. Run formatting and lightweight tests locally; run build/race/integration and
   release artifacts in GitHub Actions.

Rollback is additive: unload the plugin and remove its rule routes, rename the
dedicated ready root out of static mapping, then optionally delete owned data.
