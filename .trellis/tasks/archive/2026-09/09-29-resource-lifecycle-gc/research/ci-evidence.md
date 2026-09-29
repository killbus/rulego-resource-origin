# GitHub CI validation — complete

Final source: 804d88e733848564fda07651b698d6904a5b9d45, feat/resource-lifecycle-gc.
Run: https://github.com/killbus/rulego-resource-origin/actions/runs/36604788001
Manual dispatch of .github/workflows/ci.yml. Inspected GitHub metadata reports
completed/success at that exact head; last job completed 2026-09-29 17:27:20 UTC.

| Job | Job ID | Result |
| --- | --- | --- |
| test | 109530936170 | Metadata, formatting, go vet ./..., go test ./..., go test -race ./... passed |
| build amd64 | 109530936556 | Pinned SDK build, checksum/ABI sidecars, matching runtime smoke-load and native HTTP passed |
| build arm64 | 109530936488 | Same checks passed on native arm64 runner |
| release-metadata | 109530936583 | Metadata artifact upload passed; no release publication |

Both downloaded receipts in ci-final-artifacts/ explicitly include process-kill
restart preservation, graceful restart preservation, REST 202/307/404, native
GET/206/416/304, parent-child expiry, autonomous expiry, terminal GC to 404,
terminal catalog removal and trash removal. Both observed terminal 404 only;
neither observed transient 410.

Runtime on both architectures:
ghcr.io/killbus/rulego-server@sha256:8594e773b9d0cf2afa1fd8af0744b9eea5a500006e8847c149ed36cc1fcb559a

Plugin SHA256 from receipts:
- amd64: c10a7b5a9913d9a5a62eabb63cb03efa354f564eae23b074d9ce13eebffd7383
- arm64: 9f6efbc75f6fa51b773bfd9ca0c77540b1fbd351832e868b35a371212e37232a

## Historical candidate/transport evidence

Initial run https://github.com/killbus/rulego-resource-origin/actions/runs/36595648624
passed at 153ab1d34a5b4ec894b53622adce037af827f0b3. ci-initial-artifacts/ remains
preserved. It predates final supplemental/process-kill tests.

Initial Git Credential Manager push hung and was interrupted. Later pushes used
the authenticated gh helper for that command only, without storing credentials
in repository configuration/evidence. An initial dispatch approval and the first
final push approval timed out; permitted retries succeeded. These were timeouts,
not unsafe-action verdicts.

## Scope and limits

Independent Windows candidate tests/vet passed (verification-review.md). Main's
two real killed-subprocess windows passed (crash-evidence.md); final Linux unit/race
also executes these cases. The pinned-runtime kill separately proves healthy-ready
preservation, not interruption precisely at the two instrumented unit barriers.
Historical Windows teardown failures/noisy performance remain preserved; CI success
does not erase or explain them. Process crashes are tested, not power loss or SLOs.
