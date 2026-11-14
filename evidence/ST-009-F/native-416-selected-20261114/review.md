# Native416 download and recovery failure

Initial capture15.92s stopped by ScreenCaptureKit -3821; all16 sampled frames reviewed. Native drag and progress visible5..15s. No claim of continuous coverage to failure. Follow-up5.083333s reviewed all6 samples: -8084 error visible, foreground changes from Codex to Finder at3s. Native exact error persisted in final-error.json. The error contains no filename, so the strict filename watcher intentionally did not match.

After fault rules were cleared and error dismissed, Finder copied the same cloud file to a fresh local directory. Recovery30.071667s reviewed all31 samples: new -8084 error visible5..30s. Local result remains9MiB but incorrect SHA-256; it is explicitly a failed download, not a silent successful file.

Diagnostic after failure: direct loopback WebDAV GET200 returns full9437184 bytes with expected hash b8b5dabaa3454f7fa46a97c51cec9ccd118ffc0d61df6e46d845ef200a2fd1bb. Mounted read returns only294912 bytes without raising an exception. This demonstrates a persistent mount read failure after transient injected416. Existing protocol evidence shows206 headers followed by a truncated body on upstream416; investigate lazy opening/response headers and webdavfs caching. Root cause and fix not yet proven. Native range_416 recovery FAIL. All original failed downloads retained; no cloud deletion or modification performed.
