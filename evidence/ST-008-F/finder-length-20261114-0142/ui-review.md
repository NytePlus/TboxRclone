# Malformed input rejection with native Finder control — PASS

Three protocol-only malformed streams were sent to a fresh collection after
a valid same-directory PUT/GET control succeeded. All returned HTTP 405,
followed by HEAD 404, in 0.0005..0.0017 seconds per case. The cases are a
short Content-Length stream, chunk data exceeding its declared chunk length,
and a failed/incomplete chunk stream. Ordinary Finder cannot generate those
invalid HTTP frames; this is the explicit protocol supplement in C-008.

Finder then saved finder-control.txt into the same mounted collection. The
89.987-second recording covers the requested 90-second interval. Event-focused
review inspected 0..9 and 70..89: drag is visible at 6s, progress remains through
80s, then disappears at 81s. The final target list shows the two accepted files
and no rejected names; no error dialog appears in the reviewed completion
interval. Intermediate frames remain available, not claimed as all inspected.

verification/reopen.json proves the actual source inode, mounted full read and
independent cloud full read contain 6016 bytes with SHA-256
c1779aa910b2cb59e8eb3fee72d49b29f87aacb8af38f4635d460e93236f1b30.
source-staging.json records the hard-link alias used for source verification;
UI and verification source paths refer to the same device/inode. The first
window-opening attempt failed before dragging because titles matched; its
logs are retained in finder-open-failed.

absence.json verifies readable mount enumeration and a full independent cloud
inventory: only valid-control.bin, finder-control.txt and its AppleDouble
sidecar exist. Both accepted files match cloud hashes; short-eof.bin,
excess-bytes.bin and reader-failure.bin are absent from both views.

This does not claim Finder itself emitted malformed HTTP, nor does it certify
AppleDouble round-trip metadata or interrupted local source-file semantics.
