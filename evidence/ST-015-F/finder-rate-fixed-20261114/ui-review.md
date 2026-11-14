# 429 rerun after PROPFIND fix

Full60.093s rejection recording reviewed in7 contact sheets: progress remains at60s. Separate30.103s follow-up reviewed in4 sheets: error dialog visible throughout, source retained, destination empty. There is a recording gap; exact time the error appeared is not established. User-provided screenshot at14:26:43 confirms error code -50. Independent cloud inventory is empty and journal placeholder is Rejected. No recovery upload yet.

Initial exact error-code dialog helper did not match and performed no action; do not claim it dismissed the dialog.

The exact-text helper matching 发生意外错误 successfully pressed 好 before retry. Recovery capture stopped at35.325s of120s; all36 sampled frames reviewed and copying remains active at35s. The first cloud integrity probe observed a zero-byte placeholder while mounted content was8960 bytes; this is an in-progress observation, not a completed-file corruption verdict. Original mismatch is retained in recovery/verification-after-interruption/reopen.json. A separate60s completion follow-up was started without another upload.

Completion follow-up: reviewed all61 one-second sampled frames across7 contact sheets (0..60s of60.018333s). Source and target file visible throughout; no copying progress or error dialog. Final independent content verification matches all8960 bytes and SHA-256 across source/mount/cloud. Journal reports Committed. PASS for rejection and subsequent recovery; there is a capture gap and exact completion time is unknown. No extra upload was initiated for the follow-up.
