Verdict: PASS for recovery after controlled upload-session expiry responses.

Reviewed every one-second sample, 0..179, of the 179.993-second recording.
Finder stays visible throughout. The copy gesture occurs at about 6..8 seconds;
the progress window appears at 10 seconds and disappears at 155 seconds. The
destination remains populated and no error dialog appears through the final
sample. This is review of one-second samples, not every encoded video frame.

The UUID-scoped part-2 rule returns 403 (event 1474). Only after that rule fires,
the upload-state query returns 404 (event 1481), avoiding the initial zero-byte
placeholder. The 9 MiB operation changes upload-session hash from 73f9191c839c
to d7d4d82be1e8 and reaches Committed with three acknowledged parts.
reopen.json confirms matching source, mounted readback and independent direct
cloud download: 9437184 bytes, SHA-256
b8b5dabaa3454f7fa46a97c51cec9ccd118ffc0d61df6e46d845ef200a2fd1bb.

This models the server's expired-session response; it is not an elapsed-time
measurement of the provider's real expiry policy. Earlier recordings with
incomplete coverage or stale error dialogs are retained but excluded.
