# Direct Finder copy after latency fixes

Copy completion occurred 110.18–113.18 seconds after the drag finished, bounded by consecutive native accessibility observations (progress-watch.json). Prior one-shot-proxy baseline was still copying over thirteen minutes after dragging; do not treat its late polling time as an exact completion time. These runs compare the combined runtime/test changes, not one isolated code change.

All six main files match source, mounted reopen and independent cloud full-read SHA-256. Both valid resource forks and custom xattrs match. All three opaque/malformed forks remain missing; ST-014-F is therefore not marked PASS. See *.metadata.json. No new server ERROR occurred and all six pre-existing pending records/payloads remain byte-identical. The current service is direct rclone WebDAV → SJTU, directory cache 5s, file-content cache off.

Native evidence: scenario retained-recording.mov index contains the 90-second initial segment and 45-second continuation. There is a gap, so continuous capture is not claimed. Structured progress observation recorded the finish; the continuation end frames show no copy or error dialog. The entire movie was not reviewed frame-by-frame; no full scenario UI acceptance claim is made.

The 2-minute small-file latency is materially improved but remains a performance gap. Cloud-confirm and independent content verification were retained. No deletion/move tests were run.
