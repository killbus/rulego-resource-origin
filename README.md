# rulego-resource-origin

`resourceOrigin` publishes complete producer outputs as bounded, expiring HTTP
resources. It owns filesystem lifecycle and publication state; RuleGo's native
`resource_mapping` serves committed bytes and byte ranges.

The plugin is source- and transformation-neutral. It does not run a producer,
serve HTTP, interpret media, or download upstream resources.

## Install

Use the `.so` whose architecture and ABI sidecar match the plugin-enabled
RuleGo runtime. Place it in the runtime's `data/plugins` directory and restart
RuleGo. Release artifacts are built and smoke-loaded with the immutable SDK and
runtime in [`plugin-abi-release.json`](plugin-abi-release.json); ad-hoc Go
plugin builds are not deployment-compatible evidence.

Expose only the ready directory through RuleGo server configuration:

```ini
resource_mapping = /resources/=./data/resource-origin/ready/
node_pool_file = ./node_pool.json
```

An owner entry in `node_pool.json` is an ordinary RuleGo chain containing one
shared node:

```json
{
  "ruleChain": {"id": "resource-origin-pool", "name": "Resource origin pool"},
  "metadata": {
    "nodes": [{
      "id": "resource-origin",
      "type": "resourceOrigin",
      "configuration": {
        "root": "./data/resource-origin",
        "staticUrlPrefix": "/resources",
        "maxRetainedBytes": 1073741824,
        "maxResourceBytes": 268435456,
        "maxTtlMs": 86400000,
        "maxProductionMs": 600000
      }
    }]
  }
}
```

Rule-chain instances borrow that manager with only:

```json
{"root": "ref://resource-origin"}
```

One process and one local filesystem root form the coordination boundary. Do
not configure multiple owners for the same root or place it on a network
filesystem. RuleGo Server `v0.37.0` returns `405` for `HEAD` on static mappings;
`GET`, conditional `GET` through `Last-Modified`, and valid/invalid byte ranges
work, while clients that require `HEAD` need that capability from the host.

## Operations

Every input is strict JSON and includes `operation`.

Acquire reserves one deterministic resource identity for `key` plus
`fingerprint`. The first caller receives `state: pending`, an opaque
`generation`, and `stagingDir` on the `Produce` relation. Equivalent concurrent
callers wait for the same generation and receive its terminal result.

```json
{
  "operation": "acquire",
  "key": "caller-owned-logical-key",
  "fingerprint": "caller-owned-transformation-fingerprint",
  "ttlMs": 300000,
  "maxBytes": 104857600,
  "productionTimeoutMs": 60000,
  "parentResourceId": ""
}
```

The producer writes only beneath `stagingDir`, closes every file, and then
submits the entrypoint. `commit` discovers all members itself; callers never
supply a member list.

```json
{
  "operation": "commit",
  "resourceId": "<resourceId from acquire>",
  "generation": "<generation from acquire>",
  "entrypoint": "artifact.bin"
}
```

On producer failure, terminate the current generation without exposing its
staging files:

```json
{
  "operation": "fail",
  "resourceId": "<resourceId>",
  "generation": "<generation>",
  "kind": "producer_failed"
}
```

`resolve` is a pure read. Omit `member` to resolve the entrypoint, or provide a
committed relative member path:

```json
{"operation": "resolve", "resourceId": "<resourceId>", "member": "artifact.bin"}
```

Ready descriptors include `resourceId`, entrypoint, members, aggregate size,
publication time, absolute expiry, and static URL. The manager rejects stale
generations, traversal, symlinks, non-regular members, missing entrypoints, and
configured byte/time-limit violations. Child expiry never exceeds its ready
parent's expiry.

## REST projection

Use `resourceOriginResponse` as the endpoint output processor. It maps the
node result without serving bytes:

| Result | HTTP |
| --- | --- |
| ready | `307` with `Location` pointing at native static mapping |
| pending | `202` |
| failed | `502` |
| expired | `410` |
| not found | `404` |
| invalid input/publication/conflict | `400` or `409` |
| capacity exceeded | `507` |
| wait/production timeout | `504` |

[`examples/resource-origin/chain.json`](examples/resource-origin/chain.json)
shows the generic `acquire → Produce → producer → commit` contract. Its comment
node marks the only integration point a real producer must replace; it is not a
working file producer by itself.
