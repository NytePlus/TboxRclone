# 503 native rejection evidence

Reviewed all61 sampled frames in7 contact sheets from60.075s capture. Drag begins around6s; copying remains visible through17s. Native Finder error dialog appears at18s and remains visible through23s. Desktop switches to Codex during24..45s; Finder error visible again46..60s. This interval is not evidence of continuous Finder visibility. Source remains present; destination appears empty. Independent cloud inventory confirms no files or directories. Source hash preserved by facts.json. Journal retains zero-byte placeholder in InitSent, with no upload identity; it must not be discarded or force-marked Rejected. Full source content remains local, not in this placeholder spool.

Native failure/source retention observed. Same-path recovery and bounded retry acceptance remain incomplete; http_5xx is not PASS. Existing four pending records and this new InitSent record must be preserved.

## Retry after clearing503

Native error text independently read through Finder accessibility: error-36. Dismissed using exact fixture filename. Retry captured19.833s of45s before recorder interruption; all20 sampled frames reviewed. Progress at6..12s, local SecurityAgent password prompt at13..19s. Cancelled with exact resource-control.txt match (SecurityAgent helper success); initial Finder-only cancellation did not act. Journal still InitSent with no new operation. Root cause: untyped ErrPending becomes PUT405. Regression tests reproduce for Prepared/InitSent/Uploading/CommitSent/Unknown; backend mapping to423 under validation. This retry is FAIL for usable recovery/error reporting; no success claimed.

##423 mapping validation

Backend regression across five pending states failed before fix and passes after fix; complete backend race/vet pass. Restart preserves all five pending records and payload hashes. Same-path native retry was recorded45.093s (all46 sampled frames reviewed) but no progress/error dialog is visible in recording, so this capture does not prove visible rejection. Service logs show ErrPending and no cloud initialization was observed; journal unchanged. Exact resource-control.txt Finder dialog helper subsequently matched and pressed 好. Do not infer captured visible error or acceptance from this alone. Mounted/direct directory probes all succeed. Additional UI evidence required.

## Final 423 native verification

Recovery-423-visible: reviewed all46 samples of45.061667s. Finder source/target at0..13s, copy progress8..13s; foreground switched to Codex14..45s. Native accessibility read at07:21:37Z identified the exact fixture error: 无法完成此操作，因为项目“resource-control.txt”正在使用中。 A late watcher timed out, so no early-stop saving is claimed for this run.

Raised that same dialog without reuploading; dialog-after.json persists its exact text. Follow-up dialog.mov5.036667s reviewed all six samples0..5s: Finder error visible throughout, no password prompt. Separate recordings have a gap. Source8064 bytes/hash preserved; journal byte-equivalent sanitized snapshot still InitSent; independent cloud inventory empty. Outbound events during this retry contain no write methods, proving no repeated initialization.

ST-015-F expected contract is explicit failure, retained recoverable source and bounded retry, not successful replay of ambiguous initialization. These checks pass. Same-path upload remains blocked pending reconciliation; this is not evidence of successful automatic recovery. Initial ambiguous init must remain retained per docs/api.md section9.
