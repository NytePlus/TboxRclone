# Lazy content-open response regression

Native416 run revealed failed recovery: WebDAV reads correct full content, mounted reads remain truncated. Protocol fixture previously recorded206 plus an empty/truncated body when upstream416 occurred.

Added TestReadOpenFailureDoesNotSendSuccess with a real VFS-backed filesystem whose object Open fails. Before change, both cases failed: complete GET returned200 and valid single-range GET returned206 instead of5xx. Tool output captured the two assertion failures. After change, both pass and clearing the injected failure allows the same server to return exact expected bytes.

GET success headers are delayed until the first body write. If the handler finishes without data and the VFS read failed, the response is502 with representation headers removed. Successful content streams without full-file buffering. This does not retroactively repair failures after bytes have already been sent, nor prove all multipart-range failure cases safe.

Added boundary coverage for HEAD, empty files and truly unsatisfiable ranges without opening remote data. Whole WebDAV package race tests and vet pass (race-vet.log); patch replay matches all modified files byte-for-byte (patch-replay.json). Native acceptance remains FAIL pending new end-to-end evidence. No claim yet that this alone resolves webdavfs recovery.
