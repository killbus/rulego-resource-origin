# Journal - killbus (Part 1)

> AI development session journal
> Started: 2026-08-27

---


## Session 1: Release resource origin v0.1.0

**Date**: 2026-08-27
**Task**: Release resource origin v0.1.0
**Branch**: `main`

### Summary

Implemented and released a source-neutral resourceOrigin RuleGo plugin with atomic publication, bounded retention, restart reconciliation, native static mapping, REST status projection, ABI-pinned CI artifacts, and Docker/HTTP/media integration verification.

### Git Commits

| Hash | Message |
|------|---------|
| `f208cd340e1a5a2d41382160ece5b4f7f4292628` | (see git log) |

### Status

[OK] **Completed**


## Session 2: Trash cleanup recovery validated on both Linux architectures

**Date**: 2026-09-28
**Task**: Trash cleanup recovery validated on both Linux architectures
**Branch**: `fix/trash-cleanup-recovery`

### Summary

Implemented generation-owned cleanup retries and independent catalog recovery without changing ready-byte capacity semantics or introducing a trash quota. Local unit/vet/race and independent review passed; retained the earlier unreproduced Windows TempDir teardown failure in evidence. Explicit push/CI authorization received. CI 36431435634 passed test, amd64, arm64 and release-metadata for a1d33ce. Downloaded both plugins and verified SHA-256, ABI, platform, pinned runtime and HTTP receipts. A1-A7 complete; task archived. Production export unchanged. PR, merge and release not performed.

### Git Commits

| Hash | Message |
|------|---------|
| `a1d33cec0c69b221e1ae6aaff25ff75c3912bce3` | (see git log) |

### Status

[OK] **Completed**


## Session 3: Resource lifecycle GC verified in GitHub CI

**Date**: 2026-09-30
**Task**: Resource lifecycle GC verified in GitHub CI
**Branch**: `feat/resource-lifecycle-gc`

### Summary

Completed A1-A7: terminal catalog GC, bounded idle residue scans, generation/path safety, diagnostic regression fixes, independent baseline and mutation controls, real subprocess crash recovery. Final GitHub CI 36604788001 passed Linux unit/vet/race and pinned amd64/arm64 native HTTP plus SIGKILL restart. Preserved historical Windows teardown failures and noisy performance limits; task archived with receipts.

### Git Commits

| Hash | Message |
|------|---------|
| `153ab1d` | (see git log) |
| `804d88e` | (see git log) |

### Status

[OK] **Completed**
