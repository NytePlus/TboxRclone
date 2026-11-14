# Fixed-header native416 rerun

Fault rules armed only after source selection and endpoint checks. Complete30.101667s capture reviewed at one-second intervals (31 frames). Native drag at4..5s; target appears at5s; error dialog visible15..30s. Computer Use subsequently confirmed the exact native text: 无法完成此操作，因为发生意外错误（错误代码100004）。 The strict filename matcher does not match this filename-free dialog. A first accessibility observation returned no dialog; a subsequent native helper and Computer Use both identified the error. No early-stop saving is claimed.

One injected416 observed; zero-byte failed destination retained. Fault rules cleared by runner. Exact100004 dialog dismissed for recovery. No service restart or remount between fault and recovery. Native recovery started once into a fresh local destination; verification pending.

## Recovery verified

Recovery30.063333s recording reviewed all31 samples: copy progress6..30s. Content verification after capture shows downloaded file and mounted reopen both9437184 bytes and SHA-256 b8b5dabaa3454f7fa46a97c51cec9ccd118ffc0d61df6e46d845ef200a2fd1bb, equal to independent cloud full-read baseline. Final4.993333s follow-up reviewed all5 samples: target visible, no progress/error. Recordings have a gap; exact completion timestamp is unknown. No restart/remount was needed between injected failure and successful native retry. Range416 native failure detection and reopening recovery verified.
