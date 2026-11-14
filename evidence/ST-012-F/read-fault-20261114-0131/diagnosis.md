# Read failure diagnosis

The restart-locks run rejected the expired token with 412, but its following
GET returned HTTP 200 after 30.034 seconds and did not match expected content.
That original probe did not save headers/body; the exact returned bytes and
transport completeness cannot now be reconstructed. The same file was read
correctly after restart, before any subsequent successful PUT to that file.
This is evidence against persistent mutation, but not proof of the original
read-failure cause.

A controlled data-plane 503 reproduces HTTP 200 headers with an incomplete
body: Content-Length 43, IncompleteRead(0 bytes read, 43 more expected).
The event log proves the proxy returned synthetic 503 to the content GET.
Control and recovery reads both contain the expected 43 bytes and SHA-256.
Thus HTTP 200 alone cannot establish download success; the framing exposes
failure, and no server-side content corruption was established by this probe.

Python HTTPResponse.read(amt) does not reliably raise on a short fixed-length
body. verify-locks.py now records declared/actual length and hash, saves GET
bodies, and explicitly rejects a length mismatch as transport incomplete.
A mismatch no longer silently becomes a claim of changed object contents.

The reusable read-fault.py also exercises withheld and cut responses with
bounded reads, preserves all facts, releases held responses, and clears its
rules in finally. This tooling is supplemental protocol evidence, not Finder
acceptance. The original unexplained observation remains retained; ST-012-F
is not promoted solely on subsequent passing runs.
