# SIGKILL recovery — PASS for the declared durable boundary

All 181 one-second samples (0..180) were reviewed across the review sessions.
Finder drag occurs at 5..8s, preparation at 9s, and copy progress at 61s.
At 106s the destination window and progress disappear following SIGKILL.
This is interrupted copying, not normal Finder completion. No error dialog is
visible in the sampled frames. The source remains visible through 180s.

The second data-part response was held (request 785). Before SIGKILL at
104.69s the complete 9437184-byte durable spool matched the source hash.
The service exited 137; the journal and full spool survived. A separate
resume process preserved operation ID, upload identity and confirm key,
reached Committed at 118.20s, then the service restarted at 118.27s.

After recording, the runner explicitly unmounted, mounted and checked the
WebDAV mount. verification/reopen.json confirms source, mounted full read
and independent cloud full read have identical size and SHA-256:
b8b5dabaa3454f7fa46a97c51cec9ccd118ffc0d61df6e46d845ef200a2fd1bb.
The later remounted-finder.png captures the directory before loading;
remounted-finder-settled.png was visually inspected after loading and shows
the recovered target and retained source, both 9.4 MB, without a progress
or error dialog.

This proves explicit recovery of durable data after process SIGKILL, not
transparent Finder continuation or automatic service-startup recovery.
Host crash/power-loss is NOT_RUN and is not replaced by container SIGKILL.
