# SIGTERM recovery — PASS for the declared durable boundary

Reviewed every one-second sample 0..152 of the 152.047-second recording.
The drag occurs at 6..8s; Finder displays preparation from 11s and copy progress
from 55s. At 101s the mounted destination window and copy progress disappear;
the mount is no longer present. This is an interrupted copy, not a normal Finder
success. No error dialog is visible in the one-second samples. The source remains
visible and intact through the end. Recording ended early relative to the requested
180s; its cause is unknown. It covers the signal and service recovery, but does not
cover the later remount. That step has separate facts and screenshot evidence.

The controller held the response of the second data part (request 608), verified
the full 9 MiB spool against the source, then sent SIGTERM at about 89.7s.
The service exited 143. First and third part receipts and the entire spool
survived. A separate tbox-state --resume process used the same operation,
upload identity and confirm key; Committed was verified at about 114.0s.
The service was restarted with its original container configuration.

The initial automatic reopen check refused to inspect an absent WebDAV mount;
verification/result.json retains that failed attempt. After explicitly mounting
again, mount-webdav-macos.py check verified the exact loopback WebDAV mount.
remount-reopen.json confirms source, mounted full read and independent cloud
full read all contain 9437184 bytes with SHA-256
b8b5dabaa3454f7fa46a97c51cec9ccd118ffc0d61df6e46d845ef200a2fd1bb.
remounted-finder.png was visually inspected: the target row is present at 9.4 MB,
the local source is present at 9.4 MB, and there is no progress/error dialog.

The acceptance is recovery of already durable data after process termination,
using the documented explicit resume and remount workflow. It does not claim
transparent Finder continuation, automatic recovery on service startup, or host
power-loss resilience. Source, journal and complete spool are retained.
