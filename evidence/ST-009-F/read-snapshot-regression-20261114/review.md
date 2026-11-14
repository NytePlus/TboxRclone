# Read version regression

Before the fix, both delayed first read and forced seek reopen switched from the captured old object to the new directory object (before.log). ReadFileHandle now retains its original fs.Object for open, reopen and checksum verification. A fresh handle still reads the new object. after.log passes the targeted race suite.

The first full VFS run failed only TestWriteFileHandleReadonly because the Docker service runs as root, which can write chmod-readonly files. The full race-instrumented VFS test binary was then run as nobody with its own temporary HOME; it passed. Vet passed. Cleanup warnings from asynchronous VFS cache teardown remain visible in the log and were not hidden. Project-wide go test -race ./... and go vet ./... passed. Patch replay from the pinned upstream file matches both source files byte for byte.

Live protocol: the earlier external replacement left a newly opened WebDAV read using old metadata and returning a truncated empty body. Both project WebDAV services now use --dir-cache-time 0s. The fresh live fixture returned412 for the old ETag and23552 bytes with the new SHA-256 on reopen. This configuration increases metadata requests; performance acceptance ST-018 remains outstanding. The fixture and all pending journals are retained.

These tests do not replace native Finder recordings. ST-009-F remains NOT_RUN until native fault and recovery evidence is complete.
