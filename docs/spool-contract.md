# Upload spool contract

`journal.Spool` provides Prepare, Open and Release. Journal persistence and pruning
use this contract; transfer and recovery consume `SpoolReader` through Store.Data.
The reader supports ReaderAt for concurrent multipart sections as well as Reader,
Seeker and Closer. No transfer code opens a payload filename itself.

`DurableFileSpool` preserves the existing full `<operation-id>.data` copy,
reservation, size checks, SHA-256, fsync and ambiguous-publication retention.
An absent spool_kind in an older journal still selects this implementation.
Unknown spool kinds fail closed.

`Store.PrepareSource` explicitly selects `SourceFileSpool`. Its journal stores
absolute path, device, inode and mtime, with size and SHA-256 in the record.
It writes no `.data` payload. Every open (including recovery) verifies the opened
file and current pathname, then hashes the complete source. Missing files,
replacement inodes, changed size/mtime/digest, symlinks and nonregular files are
rejected. Nonblocking, no-follow opening prevents a replacement FIFO from hanging
recovery. Reads also check identity before and after consuming bytes. Transfer
reopens and verifies the spool before confirm. Callers must keep borrowed sources
unchanged throughout upload; this is not a filesystem snapshot or a writer lock.

Only successful independent full remote-content verification records Committed.
A second spool validation after remote readback rejects local changes during
that network operation before recording Committed. This is still not a lock
against a caller modifying the source after validation.
A lost confirm response is reconciled read-only; it does not replay confirm.
Pruning rejects uncommitted operations. Source Release never deletes the
caller-owned source, even after a verified commit. Existing streamed/WebDAV input
continues to select DurableFileSpool; source mode requires a trusted local path.

| Condition | Coverage |
| --- | --- |
| Both implementations: ReaderAt, persistence across fresh process, precommit release rejection, idempotent release | TestSpoolContract / TestSpoolProcessReader |
| Source metadata only, caller-owned file retained after release | TestSpoolContract |
| Missing/replaced/resized/retimed/modified source, symlink, FIFO, directory | TestSourceChangesFailClosed |
| Source replaced after opening a reader | TestSourceOpenReaderRejectsReplacement |
| Both implementations: multipart success | TestMultipartRoundTrip |
| Lost part acknowledgement and reopened journal, no redundant part upload | TestMultipartLostPartAckSurvivesRestart |
| Missing part after journal reopen, upload only missing part | TestSpoolMissingPartResume |
| Confirm response lost, journal reopen, reconciliation without replay | TestMultipartLostConfirmReconcilesWithoutReplay |
| Source resized, missing, replaced with identical bytes/mtime, or digest changed with size/mtime preserved before part or lost-confirm recovery; no new init/renew/part/confirm request, no persisted state transition or release | TestSourceResumeRejectsChangedSource (8 branches) |
| Remote hash mismatch prevents commit and retains local content | TestSpoolRemoteMismatchRetainsContent |
| Local spool changed during remote readback, including equal size/mtime source edits; normal and lost-confirm paths reject commit and release for both spool implementations | TestSpoolChangedDuringRemoteVerification (4 branches) |

Validation: `docker compose run --rm go-tests` runs `go test -race ./...`.
These code-level tests do not constitute Finder system-test acceptance or a
physical power-loss test.
