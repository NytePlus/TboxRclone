# Extension control: incomplete transfer, no metadata verdict

Fixture: `/private/tmp/tbox-fork-extension-20261114/ForkExtension`.
Identical valid forks in valid.bin/valid.dat; identical opaque forks in opaque.dat/opaque.bin/opaque.txt. Reproduction now supported by `scripts/finder/resource-fork-fixture.py --extension-matrix DIRECTORY`.

The first sandboxed attempt could not enumerate displays and never dragged. The native attempt delivered a verified single-selection Finder drag. The 30.11-second movie was reviewed at every one-second sample (review.0/10/20/30.png). Copy preparation remains visible at the recording end; no success is claimed.

The early *.metadata.json probes ran before any child file existed and therefore are NOT evidence of lost resource forks. The directory listing subsequently completed empty. Request trace shows MKCOL followed by repeated directory AppleDouble LOCK/GET/PUT/UNLOCK, without child PUT at that checkpoint. Proxy events show continued origin responses and no held requests. Investigate directory preparation latency before retrying or interpreting metadata results.

All five pre-existing pending journal records and their payloads match the baseline (pending-after.json). No service restart or deletion performed. ST-014-F remains unresolved.
