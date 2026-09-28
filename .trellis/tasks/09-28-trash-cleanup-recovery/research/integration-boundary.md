# Integration validation boundary

The existing CI only smoke-loads the plugin. A7 also requires publication,
native static HTTP, expiry and restart verification. tests/e2e-origin.py adds a
small disposable-runtime harness using the candidate .so and matching pinned
runtime; .github/workflows/ci.yml executes it on the existing native amd64 and
arm64 jobs. It checks the ABI sidecar and emits a receipt containing artifact
hash, platform, runtime digest and completed checks. Data lives only under a
fresh tmp/origin-http-* directory and a uniquely named temporary container.

The test submits acquire/commit/resolve to a fixture chain. A fixture producer
installs a closed file under the exact issued generation path using docker cp
with UID/GID 65532. It validates REST 202/307/404/410, static GET/206/416/304,
parent-capped child expiry, autonomous expiry without origin requests, empty
trash after healthy cleanup, and preserved ready bytes across restart. The
application still uses the host's static server; no HTTP implementation or
producer is added to the shipped plugin.

CI triggers on fix/trash-cleanup-recovery to keep iteration off main. The
workflow also supports manual dispatch once that definition is on the default
branch. Neither workflow modification executes remote CI without a push.

This Windows host does not have docker on PATH. Python command-line parsing
has been checked locally. The full harness and Linux ABI gates have NOT been
run; those remain acceptance requirements for a candidate branch commit.
