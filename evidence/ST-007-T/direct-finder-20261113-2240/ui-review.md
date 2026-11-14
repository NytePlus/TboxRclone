# Finder multipart recovery review

The original fault/display-1.mov is retained (169.743333 seconds). Reviewed one-second contact sheets cover t=0..169; t=0..59 was reviewed before the handoff and t=60..169 afterward. This is sampled video review, not a claim to have examined every video frame.

The recording shows one selected fault-recovery.bin dragged into the mounted destination around t=6s. The copy progress dialog appears around t=11s and remains visible through t=102s. At t=103s it disappears; by t=104s the target row is no longer dimmed and shows 9.4 MB. Both source and destination retain the file through t=169s. No error dialog appears in the reviewed samples. A fresh Computer Use Finder observation after recovery shows the source files retained, 9.4 MB each, with no copy/error dialog.

The outbound fault proxy held request 208 (part 2) after the real origin completed HTTP 200, from 14:43:03.368301049Z until 14:43:37.914932968Z, then dropped its response. Finder still connected directly to rclone WebDAV; no inbound adapter was used. Other parts could complete concurrently.

journal-held.json records the 9437184-byte operation as Uploading, with receipts for parts 1 and 3 but no part 2 ETag. journal-after-drop.json records the same operation and upload-ID digest as Committed, with all three receipts. Parts 1 and 3 preserve their ETags. events-final.json has no received PUT after dropped_after_release: the original remote parts were reconciled, with no repeated data PUT. This exercises a bounded part-response interruption with the session valid, not process restart, session expiry, or whole-network outage.

fault-reopen.json and fault-cloud.json independently establish matching source, mounted reopen, and full cloud-read size/SHA-256: 9437184 bytes, b8b5dabaa3454f7fa46a97c51cec9ccd118ffc0d61df6e46d845ef200a2fd1bb. Source and durable spool were retained. No deletion was performed.
