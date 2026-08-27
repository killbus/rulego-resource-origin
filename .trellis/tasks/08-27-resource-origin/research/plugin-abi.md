# Plugin ABI source of truth

The build/runtime contract is owned by the sibling `rulego-server-docker`
repository:

- `plugin-abi.lock` and the published `plugin-abi-release.json` define the ABI.
- `scripts/rulego-plugin-build` validates the manifest digest, Go version,
  CGO, target OS/architecture, compiler/linker, and shared module identities.
- `rulego-ffmpeg-over-ip/plugin.go` demonstrates the required exported
  `Plugins` registry with `Init` and `Components`.
- RuleGo Core's `types.SharedNode` and `components/base.SharedNode[T]` supply
  owner/borrower lifecycle through `ref://`; use them instead of a package
  global manager.
- `rulego-ffmpeg-over-ip/.github/workflows/ci.yml` demonstrates consuming the
  published SDK and producing `.so`, checksum, ABI sidecar, and compatibility
  artifacts.

Node-instance state is not sufficient for this plugin's lifecycle contract;
the configured owner must reconstruct valid state from its own catalog on
process start. The pure-read `resolve` operation is necessary so an HTTP route
can observe failed/expired/not-found state without `acquire` reviving it.
RuleGo's static mapping remains the byte-serving data plane.
