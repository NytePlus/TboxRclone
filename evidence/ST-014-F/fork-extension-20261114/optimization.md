# Small-file latency optimization

Baseline native copy starts 2026-11-14T08:24:22Z. Directory metadata alone postpones first child PUT until 08:27:33Z. Fault-proxy sample contains at least 1,017 cloud requests; median origin-header time 0.615 s and p95 0.875 s. The event ring is truncated and ambient Finder traffic is included: these are lower-bound service totals, not exact per-copy counts.

Changes deployed for the matched native retest:
- Existing directory ensure uses a single live full-path lookup instead of querying every ancestor. All ancestor read claims and durable pending checks remain; missing directories use the existing creation/conflict path.
- Compose directory cache defaults to 5 seconds, configurable with TBOX_DIR_CACHE_TIME; content cache remains off. External directory changes may be visible after cache expiry; this is documented.
- Live configuration snapshots capture directory cache duration for restoration.

Validation: backend race suite PASS (15.241s); backend vet PASS; WebDAV race suite PASS (35.693s); Compose config and Python syntax PASS. The broader upstream VFS suite was stopped after it began downloading unrelated backend dependencies; it is NOT claimed passed. No live timing improvement claimed before deployment and matched native copy.

Read-only transport retry follow-up: SMH race suite PASS (3.380s), transfer/backend race suites PASS, SMH/backend vet PASS. Pending deployment after the current native copy. Native optimized run hit an EOF before this follow-up and is not a successful speedup acceptance; see ../fork-optimized-20261114/observations.md.
