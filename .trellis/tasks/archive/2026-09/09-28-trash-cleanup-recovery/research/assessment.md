# Trash lifecycle assessment — 2026-09-28

## Evidence boundaries

- Source: D:/Repositories/rulego-resource-origin, main, 2489c73, tag v0.1.1.
- Production export: E:/Desktop/sxYw0hmQDtSX.json; read-only inspection only.
- The user explicitly confirms the reviewed resource-origin source is the
  production implementation. They also identify ../rulego-server-docker as the
  production runtime packaging and ../rulego as its server source. These are the
  authoritative inputs for this assessment, not unknown deployment versions.
- Source establishes control flow and conditional error consequences. No actual
  deletion incident or current trash occupancy is claimed from that alone.
- No fault-injection test has yet been run for the proposed repair.

## Production configuration

All seven resourceOrigin nodes borrow root ref://resource-origin. The owner is
not embedded in this export. node.go:63 requires explicit positive owner limits;
README example values are not defaults and are not production evidence.

| Export anchor | Setting | Meaning |
| --- | --- | --- |
| sxYw0hmQDtSX.json:654, :615 | ttlMs=21600000 | Parent resource containing initial 0.ts: 6 h after commit. |
| sxYw0hmQDtSX.json:550 | ttlMs=600000 | Demand child: 10 min, capped by parent expiry. |
| Same acquire scripts | maxBytes=33554432 | 32 MiB maximum aggregate publication per resource. |
| Same acquire scripts | productionTimeoutMs=300000 | 5 min production lease. |
| sxYw0hmQDtSX.json:641 and other resourceOrigin nodes | root=ref://resource-origin | Shared external owner; no global limit value in this file. |

Source leases and manifest cache TTLs elsewhere in the export are separate from
resource expiration and do not define trash retention. commit computes absolute
expiry from publication time and caps child expiry at the parent's deadline
(origin.go:372). Reusing ready resources does not renew their TTL (origin.go:261).

## Lifecycle and limits

1. Acquire gives a generation-specific staging directory (origin.go:290).
2. Commit scans files, checks per-resource and ready-byte capacity, renames to
   ready, persists the catalog, and increments retainedBytes (origin.go:339).
3. Expiry renames ready/<resourceId> to trash/<resourceId>-<generation>, decrements
   retainedBytes, marks expired, persists, then calls RemoveAll (origin.go:536).
4. Pending failure/timeout directly removes staging, rather than using trash
   (origin.go:518). Its ignored deletion errors are adjacent recovery concerns,
   not proof of a trash-specific production incident.

There is no independent trash-size ceiling, count ceiling, age threshold, or
waiting period. Healthy expiry immediately attempts deletion. The manager starts
an expiry worker on initialization (origin.go:193); the worker chooses the next
pending or ready deadline (origin.go:706). Acquire/Resolve also expire encountered
resources, and Commit runs a sweep (origin.go:248, :424, :339).

## Failure findings

### F1/F2 — residual trash is not retried during normal runtime

Both RemoveAll calls in expireLocked ignore errors (origin.go:542, :556).
After the resource becomes expired, expiryLoop and sweepLocked no longer select
it (origin.go:713, :749). The latter scans catalog records, not the trash directory.
Consequently, a temporary failure can leave bytes indefinitely until manager
reinitialization or external cleanup. More requests do not by themselves retry
that retired generation's trash removal.

### F3 — persistence failure also needs recovery

origin.go:546 changes accounting, and :550 changes state, before persistLocked at
:553. If persistence fails, final deletion is skipped. The in-memory expired
record is no longer scheduled. Catalog replacement itself uses a temporary file
and rename (origin.go:568). Verify interrupted transitions and reacquisition of
the same deterministic identity with a different generation; do not let retry of
an old snapshot overwrite a newer catalog record or remove a newer ready path.

### F4 — retained-byte admission does not bound physical disk use

Commit enforces size <= maxRetainedBytes - retainedBytes (origin.go:354), adding
size only after a successful publication (:382). Expiry subtracts the full size
before physical trash deletion (:546). Staging, trash, filesystem metadata and
catalog are not included. A configured ready-byte ceiling therefore does not
prevent disk growth caused by accumulating cleanup failures. Hitting that ceiling
rejects publication with storage_limit; it does not trigger LRU eviction or a
trash-size threshold. The production owner's actual ceiling remains unknown.

### F5 — restart reconciliation is a fallback, not runtime retry

newOriginManager calls reconcile before starting the worker (origin.go:190).
reconcile scans all trash entries and removes them (origin.go:671); a deletion
error aborts manager initialization (:677). Existing tests verify restart orphan
removal (origin_test.go:331) and retained-byte rejection (:486), but do not prove
recovery from an injected runtime trash-deletion failure.

## Recommendation

First repair generation-safe runtime retry, persistence-failure recovery and
bounded diagnostics. Preserve current ready-byte admission semantics and document
residual trash explicitly. A hard total-disk budget would additionally require
staging reservations, admission across producers, partial-deletion accounting and
filesystem overhead policy; it should not be disguised as a trash age threshold.

The source-based assessment and approved repair do not depend on collecting
production logs. Fault tests supply explicit operation failures to verify the
recovery contract; they are not observations of a production incident.


## Production source trace and failure timing

The runtime Dockerfile.plugin-runtime copies the app to UID/GID 65532, runs
USER 65532:65532, and exposes /app/data as its data volume. Its runtime verifier
asserts this identity. No separate writer identity is established by this path.

indexed-media producer.go creates temporary inputs inside the issued staging
directory, writes/syncs/closes them before invocation, and waits for the FFmpeg
client to return before publication. Its pinned ffmpeg-over-ip v0.5.0 client
performs file operations locally: client/files.go:106 creates parent directories
and :110 opens the file in the caller process. client/client.go:175-181 defers
closeAll before Run returns. The remote FFmpeg service is therefore not evidence
of a second container/user creating ready files in this root. The sibling client
checkout differs from this pinned dependency; this trace uses the exact v0.5.0
module used by indexed-media.

rulego/server/internal/endpoint/static.go serveStaticFile calls os.Open and
defers f.Close around http.ServeContent. It does not consult the origin catalog.
On Linux an active reader does not prevent rename/unlink of these regular files.
It may retain allocated blocks until the final open handle closes. This is not a
RemoveAll error and a retry queue cannot free those blocks before handle closure.

For a successful committed generation, application writes have finished before
the ready directory is published; origin lifecycle operations are serialized.
The inspected normal playback path therefore supplies no established concurrent
writer or ownership mismatch that forces trash deletion to fail at TTL expiry.

The code-defined trigger times are relative to state, not predictions of errors:

- Parent retirement is due at publication plus 6 h.
- Child retirement is due at min(publication plus 10 min, parent expiry).
- Uncommitted production becomes due for failure at acquire plus 5 min.
- Scheduler execution may occur after the deadline; request paths also detect
  overdue records. None of these deadlines means a filesystem call must fail.

Conditional failure F3 is especially concrete: if catalog CreateTemp/encode/Sync/
Close/rename returns an error after ready-to-trash rename, the baseline returns
before RemoveAll. Trash can therefore remain even when deletion itself would
succeed. A deterministic test can choose that exact call and invocation count.
That predicts the code response to the supplied condition, not when the storage
system will first report the condition in production.

There is also a source-defined capacity condition without prior trash failures:
ready admission omits staging and catalog, and each distinct resource identity
retains its catalog record after expiry. An unbounded stream of new identities
on a finite filesystem can therefore exhaust space/inodes even if every healthy
trash deletion succeeds. A wall-clock exhaustion time requires the input stream
and capacity, which are values in the condition rather than unknown code. Catalog
compaction and aggregate disk admission remain explicitly outside this repair.

Boundary adjacent to pending cleanup: client/files.go recreates parent directories
for an output open, and cancellation in client.go permits up to 5 s of grace. A
late file-open request can therefore recreate an old staging path after a
production-timeout cleanup. This is not proof of a ready/trash deletion failure;
generation-safe retry must never clean a newer staging generation. Coordinated
producer cancellation is a separate contract if this race is addressed later.
