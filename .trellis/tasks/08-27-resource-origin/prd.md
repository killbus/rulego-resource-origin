# Resource Origin Plugin

## Goal

Provide one source-neutral RuleGo node that publishes complete transformation
outputs as stable, lifecycle-bound HTTP resources or resource sets. The node
owns identity, readiness, bounded retention, and restart reconciliation;
RuleGo's existing static mapping continues to serve ready bytes and ranges.

## Requirements

- Expose `acquire`, `commit`, `fail`, and pure-read `resolve` operations through one
  `resourceOrigin` node.
- Derive an opaque resource ID from caller-owned `key` and `fingerprint`.
- Share equivalent in-flight work and never expose staging files.
- Persist pending/ready/failed/expired state beneath one configured root.
- Use RuleGo's native `SharedNode`/`ref://` ownership so one configured origin
  can be reused by rule-chain node instances without a package-global registry.
- Commit only closed regular files below the issued staging directory; reject
  traversal, symlinks, stale generations, missing members, and size overflow.
- Enforce per-resource TTL/max bytes and an origin-wide retained-byte ceiling.
- Preserve valid ready resources across node recreation/process restart and
  reclaim only artifacts owned by this origin.
- Cap child resource expiry to its parent resource-set expiry and reject child
  acquisition after parent expiry.
- Return static URLs only for committed files. Do not open an HTTP listener or
  implement GET/HEAD/Range serving.
- Provide only the small REST response projection that maps origin outcomes to
  HTTP status/body/redirect through RuleGo's existing endpoint.
- Remain transformation- and source-neutral: no FFmpeg arguments, codecs,
  YouTube fields, media profiles, or player sessions.
- Build only against the immutable RuleGo Plugin ABI SDK contract published by
  `killbus/rulego-server-docker`.

## Acceptance Criteria

- [ ] Two equivalent concurrent acquires reserve one generation and receive
  the same ready resource identity.
- [ ] Commit is atomic: no member is visible before validation and rename;
  stale generations, path traversal, symlinks, missing files, and byte-limit
  violations are rejected.
- [ ] Ready state includes resource ID, entrypoint, members, size, static URL,
  publication time, and absolute expiry.
- [ ] Pending, failed, expired, and unknown resources have deterministic node
  results; stale files cannot revive an expired record.
- [ ] Restart reconciliation preserves valid ready records, abandons pending
  generations, removes owned orphans, and leaves unrelated files untouched.
- [ ] Parent/child expiry and retained-byte ceilings are covered by focused
  tests.
- [ ] An example RuleGo chain composes the node with a producer and native
  static mapping; no duplicate byte-serving layer exists.
- [ ] GitHub CI formats/tests/race-tests and builds versioned linux/amd64 and
  linux/arm64 `.so` artifacts with checksum, ABI sidecar, compatibility record,
  and a matching-runtime smoke load.

## Boundary

- FFmpeg and other nodes remain byte producers.
- Rule graphs own transformation policy and deterministic production-unit keys.
- RuleGo static mapping owns HTTP byte transfer.
- This plugin owns only publication state and filesystem lifecycle.
