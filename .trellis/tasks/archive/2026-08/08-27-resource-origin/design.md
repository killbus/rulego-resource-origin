# Design

## Public node contract

`resourceOrigin` accepts strict JSON operations:

- `acquire`: `key`, `fingerprint`, `ttlMs`, `maxBytes`,
  `productionTimeoutMs`, and optional `parentResourceId`.
- `commit`: `resourceId`, opaque `generation`, and `entrypoint`; the origin
  discovers members by scanning only the issued staging directory.
- `fail`: `resourceId`, opaque `generation`, and a bounded non-secret reason.
- `resolve`: `resourceId` and optional `member`; this never reserves or revives
  work and reports `pending`, `ready`, `failed`, `expired`, or `not_found`.

An absent acquire reserves one generation and returns a staging directory for
the graph's producer. A pending equivalent acquire waits on that generation.
A ready acquire returns its immutable descriptor. Commit validates the issued
staging tree, then atomically renames it under `ready/` before persisting ready
state. Fail and expiry wake waiters with deterministic outcomes.

## Storage

Use only the Go standard library and a single configured filesystem root:

```text
catalog/<resource-id>.json
staging/<resource-id>/<generation>/...
ready/<resource-id>/...
trash/...
```

Catalog files use temp-file plus rename. Static mapping exposes only `ready/`.
Cleanup first removes a resource from the mapped tree, then removes metadata
and bytes. Startup trusts only valid catalog ownership and never scans or
deletes outside these roots.

One configured owner holds the manager through RuleGo's native `SharedNode`;
rule-chain nodes borrow it through `ref://`. Cross-host coordination, network
filesystems, LRU, databases, object stores, package-global registries, and a
second HTTP server are out of scope.

## RuleGo composition

The node follows the normal plugin registry (`Plugins`, `Init`, `Components`),
RuleGo `SharedNode`, and `ref://` lifecycle. It exposes `Produce`, `Success`,
and `Failure` relations. A tiny `resourceOriginResponse` output processor maps
outcomes to REST status/body/`Location`; it never serves bytes. Ready
descriptors contain the configured static URL prefix. The host's native static
route supplies GET, byte ranges, and `Last-Modified` validation. RuleGo Server
`v0.37.0` does not implement HEAD for static mappings; that remains a host
capability rather than a second serving plane in this plugin.

The plugin ABI release record and SDK image from `rulego-server-docker` are the
only Go/toolchain/module compatibility inputs. No local copy of that matrix is
maintained.
