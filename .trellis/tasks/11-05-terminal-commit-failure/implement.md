# Implementation plan

## 1. Inputs and gates

- Active task:
  `D:\Repositories\rulego-resource-origin\.trellis\tasks\11-05-terminal-commit-failure`
- Base commit: `c6731ac`
- Planning approval: `approval.md` must exist with the approved design hash before
  implementation.
- The implementer must read `implement.jsonl`, `prd.md`, and `design.md` before
  editing.

## 2. Source changes

1. Add a small private helper that terminalizes a current generation while
   preserving the caller's commit error.
2. Use that helper for `invalid_publication`, `storage_limit`, ready-path
   conflict, rename failure, and catalog rollback failure after the generation
   has been validated.
3. Keep `production_timeout` and `parent_unavailable` behavior intact.
4. Make the catalog rollback path terminal on both successful and failed
   rollback, without deleting an unrelated ready tree.
5. Update the component version, `VERSION`, and `compatibility.json` to `0.2.1`.
6. Update README lifecycle language to state that terminal commit errors retire
   the generation and notify equivalent waiters.

## 3. Tests

Add focused tests in `origin_test.go` and, where needed,`node_test.go`:

- Fill retained capacity; acquire a second key; write its staging file; commit;
  assert `storage_limit`, failed state, waiter wakeup, and no context timeout.
- Assert a later acquire of the failed key returns a different generation after
  collection.
- Cover missing entrypoint as `invalid_publication` and one publish-path error.
- Preserve existing success, producer-fail, expiry, cleanup, and restart tests.
- Add a runtime E2E assertion that a capacity-limited commit returns HTTP 507 and
  a subsequent equivalent waiter does not wait for the production deadline.

Use deterministic hooks and barriers already present in the test suite. Do not
add sleeps as synchronization when an existing hook can be used.

## 4. Validation

Run from `D:\Repositories\rulego-resource-origin`:

```powershell
gofmt -w origin.go node_test.go origin_test.go
go vet ./...
go test ./...
go test -race ./...
```

Run the pinned runtime E2E through the existing CI workflow. Local Docker is not a
substitute for the pinned CI artifact, so the release evidence must come from the
owning repository's workflow.

## 5. Release

1. Confirm all tests pass at the release candidate commit.
2. Verify `VERSION`, `compatibility.json`, and `Def().Version` agree.
3. Let CI build both Linux architectures and smoke-load the plugin.
4. Record the successful run ID, artifact digests, and ABI sidecars.
5. Do not publish or deploy from the implementer sub-agent.

## 6. Review loop

Use the configured Paseo roles:

- Implementer: implement directly; do not spawn another implementer or checker.
- Reviewer: review changed code against `prd.md`, `design.md`, and the
  resource-origin spec; write only `review/findings.json`.
- Main session: resolve integration, run the final checks, and commit.

If the reviewer identifies a design gap, stop and record `review/spec-gap.md`;
do not edit the design from the implementation role.

## 7. Rollback points

| Point | Action |
| --- | --- |
| Before code change | Keep 0.2.0 deployed; no source changes |
| After code change | Revert only this task's source commit |
| After release | Keep 0.2.0 artifact as rollback target |
| After deployment | Restore prior plugin and verify components API |
