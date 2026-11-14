# Resource fork diagnostic controls

These controls investigate the failure; they do not mark ST-014-F PASS.

- Local Finder Option-drag preserves the opaque resource fork and custom xattr (`local-metadata.json`, `local.mov`, `local-drag.log`). Source remains present.
- `scripts/finder/resource-fork-fixture.py` reproduces `ForkProbe`: a valid classic resource fork containing one TEST resource (ID 128), and an opaque text fork with a different filename extension.
- Finder upload preserves the valid fork byte for byte after a fresh unmount/mount (`valid-after-remount.json`). The opaque fork is absent (`opaque-before-remount.json`), as in the original failure.
- macOS native `copyfile(COPYFILE_PACK | COPYFILE_XATTR)` preserves both forks in AppleDouble (`native-pack-control.json`, `*.appledouble`). Thus native packing itself does not discard the opaque fork.
- Direct PUT of the native-packed opaque AppleDouble, under a fresh `opaque-packed.dat` basename, preserves every sidecar byte and exposes the opaque fork through mounted `xattr` (`packed-sidecar-readback.json`, `packed-opaque-readback.json`). This is a protocol diagnostic, not a substitute for Finder acceptance.

The evidence points to the Finder-to-WebDAV copy path generating an empty fork for opaque/malformed resource data, rather than a general inability of rclone or the backend to store resource forks. No server change is justified yet. The original opaque-fork loss remains an unresolved behavior; retain it alongside the valid-format control. Same-directory case collision, Finder rename, and application-save cases remain outstanding.

The 90-second WebDAV recording ends with copying still active. A subsequent Computer Use observation found the source Finder window with no progress dialog, before remount. Do not treat that recording alone as complete UI acceptance evidence.
