# Design Critique: `engineering-design.md` v1.8

This draft is in good shape. I did one more full pass for contradictions, hidden state transitions, and example drift. I found one remaining issue worth fixing before treating the doc as implementation-ready.

## Findings

### 1. The degraded-success path exists in the daemon design but is not specified in the CLI output contract

The post-operation flow says that if local runtime-state creation/deletion fails after a successful commit, the daemon performs immediate reconciliation and the CLI returns a degraded success indicating the schedule is committed but local state recovery was needed. But the structured-output section and the success examples still show only the normal success shape, and there is no corresponding `--json` shape documented for this case. See [engineering-design.md](</Users/fredy/Google Drive/My Drive/Projects/scheduler/docs/engineering-design.md:265>), [engineering-design.md](</Users/fredy/Google Drive/My Drive/Projects/scheduler/docs/engineering-design.md:285>), [engineering-design.md](</Users/fredy/Google Drive/My Drive/Projects/scheduler/docs/engineering-design.md:415>), [engineering-design.md](</Users/fredy/Google Drive/My Drive/Projects/scheduler/docs/engineering-design.md:597>).

Why this matters:
- This is part of the external write-command contract, not just an internal recovery detail.
- Agents need a deterministic way to distinguish “clean success” from “committed, but local recovery was needed.”
- Without a documented output shape, the implementation has to invent one ad hoc, which weakens the doc’s value as an implementation contract.

Recommended change:
- Add one plain-text example and one `--json` example for degraded success.
- A minimal v1 contract could keep `status: active` and add a `recovery: local_state_reconciled` field plus a short warning/message field with the same meaning represented in JSON.

## Overall Assessment

No other design-level issues stood out on this pass. Once the degraded-success response shape is documented, the draft should be solid enough to build from without further policy invention.
