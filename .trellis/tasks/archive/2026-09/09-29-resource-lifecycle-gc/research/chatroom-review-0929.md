# Chatroom expert review — resource-lifecycle-gc (2026-09-29)

Simulated expert chatroom (dbs-chatroom) on the planning artifacts at HEAD b22d4b8. Experts read prd/design/implement and origin source where noted. Findings below are inputs to planning, not decisions.

## Leslie Lamport (state-machine/invariant audit)
- Weakest: R2 periodic reconciliation, not R1. Implicit invariants should be written down: L1 ready/<id> only created/removed under m.mu with generation-scoped trash/staging names; O1 terminal state persisted before the first payload byte is deleted.
- Proposed out-of-sync deletion ("expensive deletion outside synchronization") breaks L1: judging orphan under lock, then deleting ready/<id> outside it, races a new Acquire+Commit. Evidence is code reading; no reproduction exists yet.
- Fix direction: reconciliation may only claim paths by renaming them into a quarantine name inside the lock; deletes happen only on quarantine paths outside the lock; ID-level recursive deletion remains startup-only.
- R3 needs reservations: Acquire currently reserves nothing (evidence: Acquire does not account bytes; Commit checks retainedBytes). N concurrent producers each writing MaxBytes can exhaust disk. Reserve on acquire, correct on commit, release exactly once on failure/expiry/GC. origin-visible bytes can be a hard budget; statfs stays observational.
- Collection precondition C1: terminal ∧ removed ∧ no pending persist ∧ no record uses it as parent. Observable behavior difference narrows to Resolve expired/failed -> not_found (currentGenerationLocked already returns stale_generation for nil/terminal).
- Playback protection cannot be implemented inside origin because static requests bypass it; own it in native routing or drop it explicitly.
- prepareCleanupLocked performs rename + catalog write while holding the global mutex; a slow disk blocks all Acquires. Either declare bounded local-disk assumptions or move hide-rename to quarantine flow.

## Remzi Arpaci-Dusseau (crash-consistency audit)
- Evidence: startup reconcile (origin.go:679, :708) deletes orphans then clear(m.garbage); any partial RemoveAll failure fails startup completely. Periodic reconciliation still lacks an owned-path grammar for mixed staging/<id>/<generation>, unknown names, and trash.
- persistLocked fsyncs the temp file but not the directory; publish is a dual rename (staging->ready, then catalog). After power loss both can regress; the plan does not model this.
- Inference: the historical Windows TempDir teardown failure likely reflects fixture assumptions about old terminal-catalog semantics; single instance, keep as open question, not conclusion.
- Recommended validation: define owned-path classification + startup failure policy first, then failpoint injection at syscall boundaries (after rename, mid RemoveAll, after catalog rename, during shutdown), kill -9, reboot, assert invariants from directory scan. Linux is authoritative; Windows is supplemental.

## Marc Shapiro (convergence audit)
- Evidence: design.md admits catalog absence cannot imply orphan status before metadata-GC coordination but defers the adjudication; single-flight for reconciliation is unmentioned; check-then-delete is a TOCTOU window.
- Proposal: claim-then-delete separation. Claim (atomic rename into private quarantine under publication sync) is the linearization point; deletes act only inside quarantine and treat ENOENT as success. Repeated scans converge; a late producer rebuilding the original path simply gets claimed next round.
- Hard rules: catalog missing + path shape matches + grace period >= metadata retry cap before claiming; reconciliation runs single-flight.
- Order claim -> payload delete -> metadata collect converges under arbitrary interleavings; deleting JSON first or deleting outside quarantine breaks convergence. The idempotence asserted in design is otherwise unargued.

## Brendan Gregg (USE observability audit)
- Capacity admission is defined on the wrong denominator: maxRetainedBytes excludes temp, orphan and unlinked-but-open bytes, so a full disk can look healthy.
- Required metrics: per-state byte buckets (ready/staging/orphan/unknown), disk free + growth rate, oldest orphan age histogram, deletion failures by errno class, reconciliation drift counts, expired->not_found transitions.
- Distinguishes evidence (known code facts) from inference (no production measurement exists).
- Recommendation: measurement phase before thresholds are frozen: representative 24h run recording per-state bytes, orphan age histogram, GC throughput vs production rate; thresholds come from that data.

## Karl Popper (falsifiability / anti-audit)
- The unexplained Windows TempDir teardown failure undermines the red-test gate: a red test has an unexcluded alternative explanation until the failure is reproduced and classified on an unmodified baseline. Gate: unclean baseline invalidates red-test evidence; independent reviewers require reproducible failure records, not narration.
- TTL is currently unfalsifiable because the invariant is undecided. Write two mutually exclusive assertions (bytes of an expired resource either remain readable or become deterministically not_found) with counterexample tests before implementation.
- maxRetainedBytes is falsifiable only as "catalog-visible ready bytes <= cap". Acceptance must inject external files to fill the volume and verify a defined rejection behavior; otherwise the guarantee is self-referential.

## Judge synthesis
Convergent fixes across Lamport/Shapiro/Remzi: quarantine-claim reconciliation with single-flight, reservation accounting, crash-boundary enumeration, Linux-authoritative failpoint tests. Gregg and Popper both demand measurement-first and a clean-baseline evidence gate. Unresolved product decisions remain: TTL semantics (blocked on user), startup failure policy, and measurement environment.
