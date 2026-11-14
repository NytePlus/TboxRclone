# disconnect_last_part

Reviewed every one-second sample t=0..179 of the 180-second recording.
Drag is visible at 6..8s; copy/progress dialog at 9..92s.
The dialog is gone at 93s; ordinary file entries remain through 179s.
No error dialog is visible in these samples. Source remains present.
This is one-second sampling, not review of every encoded frame.

The selected data-part request 279 logged origin_transport_error
at 2026-11-13T15:27:03.580553049Z, 58.699s after capture launch. The rule limits
upstream body reads to 64 KiB and skips requests smaller than 1 MiB.
This proves a selected request disconnection, not whole-host network loss or
an exact count of bytes durably stored upstream.

journal.json records the 9 MiB operation Committed with three part receipts.
reopen.json confirms source, mounted reopen and independent full cloud read
agree: 9,437,184 bytes, SHA-256
b8b5dabaa3454f7fa46a97c51cec9ccd118ffc0d61df6e46d845ef200a2fd1bb.

Verdict: PASS for disconnect_last_part, with automatic recovery.
