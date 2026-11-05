# Design revision round 1

## Repairs

- G1: §4/§5/§6 now explicitly classify a non-`ErrNotExist` ready-path
  inspection failure as terminal `publication_failed`. The waiter and failed
  record use that sanitized kind, while the direct caller keeps the original
  inspect-ready-path error and cause. The design now requires deterministic
  coverage for failed state, waiter kind, direct caller error, and cleanup
  scheduling.
- G2: §5/§6 replace `publication_conflict` with the existing `conflict`
  contract. The direct caller stays `conflict`/HTTP 409 and the waiter
  descriptor uses `failureKind=conflict`. §11 requires focused acceptance
  coverage for that ready-path conflict branch.

## Sections changed

- `design.md` §4: added the ready-path inspection outcomes to the commit flow.
- `design.md` §5: changed ready-path conflict to `conflict`, added terminal
  classification for non-`ErrNotExist` ready-path inspection, and preserved
  direct-error cause behavior.
- `design.md` §6: aligned direct caller, waiter, and HTTP status contracts for
  `conflict` and `publication_failed`; required focused assertions.
- `design.md` §11: expanded the AC6 design coverage landing for both blocking
  gaps.

No PRD, implementation plan, spec, source, or release files were changed.
