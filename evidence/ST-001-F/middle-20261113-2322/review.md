# Middle data-part interruption

Reviewed every one-second sample t=0..126 from display-1.mov (compact-review.0
through compact-review.120). Single-file drag at 6..8s; copy/progress dialog at
9..62s; dialog gone at 63s and destination appears as an ordinary file thereafter.
No error dialog was visible in these samples. Source remains present. This is
one-second sampling, not review of every encoded frame.

The movie ended at 126.21s rather than the requested 180s. Unlike the earlier
first-part run, it covers the actual fault and more than a minute after completion.
The one-shot part-2 rule (minimum Content-Length 1 MiB) limited upstream body reads
to 64 KiB. Request 187 logged origin_transport_error at 15:23:38.206932509Z,
53.08s after capture launch. This is a selected data-request disconnection, not a
claim of whole-host network loss or the exact number of bytes stored upstream.

journal.json shows the 9 MiB operation Committed with three part receipts and
retained complete spool. Source, mounted reopen and independent full cloud read
match in reopen.json: 9,437,184 bytes and SHA-256
b8b5dabaa3454f7fa46a97c51cec9ccd118ffc0d61df6e46d845ef200a2fd1bb.

This establishes disconnect_middle_part. It does not establish the other required
ST-001-F subcases or change the total accepted branch count by itself.
