# Commit failure evidence

## Production observation

Source task:

```text
D:\Repositories\rulego-engineering\.trellis\tasks\11-05-hls-playback-failure-attribution
```

- At 2026-11-05 16:52:26 Asia/Shanghai, the first segment-1 request acquired
  `resourceId=0d7490e2cf16e7f081814d59b3deb387fad405b6a438b9195cbccb4b23be09e7`
  and generation `24aa6f43160ea4c807c9a56dbbdb6c20`.
- At 16:52:28 the producer wrote 3,909,272 bytes successfully. The following
  commit returned `storage_limit: publication exceeds retained-byte limit`.
- The generation remained pending. Five equivalent requests waited on the same
  resource ID and ended with `wait_timeout: context canceled`.
- `publishBy` was 16:57:26; the last visible client wait ended at 16:57:20.
- A later request after capacity release produced 3,909,272 bytes in 5.25 seconds
  under a new generation.

The full timeline and raw log snapshot remain in the source task's `research/`
directory.

## Source anchors at base c6731ac

- `origin.go:296-308`: a pending acquire waits for `wait.done` or context
  cancellation.
- `origin.go:340`: acquisition sets `publishBy = now + productionTimeout`.
- `origin.go:356-428`: commit publication flow.
- `origin.go:387-389`: retained-byte ceiling returns `storage_limit` directly.
- `origin.go:380-395`: invalid staging/entrypoint and ready-path conflict return
  without terminalizing the generation.
- `origin.go:396-421`: publication and catalog failure return without notifying
  waiters in the rollback-success path.
- `origin.go:556-568`: `failLocked` sets failed state, schedules cleanup, and
  notifies the waiter.
- `origin.go:585-590`:`notifyLocked` stores the terminal descriptor and closes
  the waiter channel.
- `origin.go:825-843`: the sweep fails pending records only when `publishBy`
  expires.
- `plugin.go:71-83`: `storage_limit` maps to HTTP 507; `wait_timeout` maps to 504.

## Scope implication

The fix belongs in `originManager.Commit`: after a valid current pending
generation is identified, a terminal publication outcome must transition it to
failed and notify its waiter. The HLS graph should not compensate by adding a
second lifecycle owner or by calling `fail` after the manager has already
terminalized the generation.
