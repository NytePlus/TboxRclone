# 429 native Finder review — latest run

Injected multipart POST 429 is recorded in facts.json. The user-supplied screenshot user-provided-error.png shows native Finder error -50, the source present, and the destination window empty. Independent pre-retry cloud inventory is empty. This screenshot supplements the rejection movie; the rejection-followup movie captured Codex and cannot establish Finder state.

Recovery: recorded movie duration 62.481667 seconds versus requested 180. All seven one-second contact sheets were reviewed. Finder progress appears at second 6; seconds 13–16 show Codex; Finder returns at second 17. Destination file becomes visible around second 37, but progress remains at second 62. ScreenCaptureKit stopped with -3821. No claim of continuous completion coverage is made.

Post-recording verification: recovery/verification-after-interruption/reopen.json shows source, mounted reopen and independent cloud full read all match: 8960 bytes, SHA-256 a496bb13299434fc00509196d12d48d1ffc389f55127f1ed62c3b86e154df88d. journal-after-recovery.json has full upload Committed, and the earlier rejected zero-byte operation retained.

Native UI observation around 13:52 local still exposes a copy progress window, 9 KB/9 KB, estimating remaining time. Therefore file integrity is verified but UI acceptance remains incomplete pending the completion follow-up. No second upload was initiated.

Completion follow-up: duration 30.076667 seconds, all four contact sheets reviewed. Copy progress remains visible throughout. At second 9 the background changes to Codex, but the Finder copy window remains visible over it through second 30. This is positive evidence of continued progress-window presence, not proof of completion. No held proxy responses remained in events-after-recovery.json. Investigate slow/stalled Finder finalization before accepting http_429.

Diagnostic sampling: Finder directory synchronization waits in getattrlistbulk / getattrlist; webdavfs_agent waits in CFReadStreamRead → waitForOpen. All matching AppleDouble journal records are Committed (including 4096-byte payloads). A separate depth-0 PROPFIND returns207 in4.34s. This narrows the investigation to directory/HTTP finalization but does not prove a root cause. Samples are finder-wait-sample.txt and webdav-wait-sample.txt.

Final observation around14:01: 15.083s movie, all16 sampled frames reviewed. No copy dialog visible, but the destination window is the mount root and has no visible entries, rather than the requested fixture directory. Opening the exact fixture with the existing helper remains pending (session2061). Therefore this does not establish successful usable Finder recovery; do not mark PASS based on absence of a progress dialog alone.

The existing open helper subsequently reported its30s timeout with no acceptance result. The blank mount-root view and inability to open the test folder remain unresolved; service/API success is not a substitute for usable native browsing.

Bounded reproduction: list-diagnosis-bounded.json records both mounted listings timing out15s then not reaping after SIGKILL (driver returned21.04s). Both direct depth1 PROPFIND calls time out15s. Proxy continues returning successful upstream GETs; no held fault response. The current http_429 verdict is FAIL for unusable native recovery, while byte integrity remains verified. This is not a claim of data loss or a proven code root cause.

After deploying the request-local PROPFIND node reuse fix and remounting: exact-folder opening succeeds. All16 one-second frames of fixed-browse.mov show source and requested destination with resource-control.txt visible, without a copy/error dialog. Independent cloud/full mount reopen verifies all8960 bytes again. Bounded root/fixture listings now succeed (see list-diagnosis-fixed.json). This repair validation does not erase the original failed recovery or substitute for a fresh429 failure/retry test.
