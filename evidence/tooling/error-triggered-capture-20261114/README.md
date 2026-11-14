# Expected-error capture optimization

Problem: resource-fault.py waited for a fixed45s capture even when Finder had already reported insufficient space, then decoded/reviewed the entire capture. This increased response latency and preview work.

For cache-full and cloud-quota runs, record.sh now rejects a matching stale dialog before dragging, starts one persistent native accessibility watcher after the drag, and polls at0.5s intervals. Both the test filename and expected no-space text must occur in the matching dialog. The watcher persists the observed text before sending a stop-file signal. Every display recorder retains3s of tail footage, stops cleanly, and validates the resulting duration. No match keeps the original maximum duration. No button is pressed and this does not assign PASS. Unexpected capture interruptions remain failures. capture-coverage.json distinguishes deliberate early stop and reports saved wait seconds.

Validation: Swift typechecking; shell/Python syntax; source-layout and diff checks passed. A nonexistent-marker native watcher timed out with exit3 and created no stop signal. A recorder-only smoke test requested45s, sent a stop signal2s after recording began, and finished3.2116s after the signal with valid5.11s media, exit0. The disposable smoke movie was removed. This is helper validation, not a rerun or PASS of the paused Finder acceptance tests. End-to-end validation was subsequently completed; see the resumed validation below.

The already-running normal-budget restoration completed; the service is back at4G. System-test work subsequently resumed; the goal remains active.

## Resumed end-to-end validation

Real cache-full run finder-cache-fast-clean-20261114: source preserved, empty journal and empty independent cloud inventory; matching no-space error visibly present at13..18s. Graceful capture18.7767s, wrapper wait19.6615s vs45s budget (25.3385s saved). Full helper elapsed31.1544s. All19 sampled frames reviewed in2 sheets instead of5. The preceding attempt correctly stopped before dragging because a stale matching error was already present; it was dismissed before this fresh run. Normal4G budget subsequently restored with all five pending journal/payload byte hashes unchanged.
