# Ignored Range protocol supplement

The initial full fixture and valid range read matched independently established SHA-256. The scoped data-plane ignore_range rule removed Range/If-Match before contacting the real origin; non-ranged GETs do not consume that rule. The backend rejected the full response as a range result: downstream WebDAV had already sent206 headers, then delivered0/3968 bytes and a detectable truncated response in1.724 seconds. No whole-file bytes were exposed as the requested range.

After clearing all rules, the full9728-byte file matched SHA-256 8d97eb42acd7f8c3c2df9b75a155b3c92b4ec7232d940cc3374b4c041aa5dc39. The same recorded ETag was present on normal reads. This is protocol evidence, not native Finder acceptance; ST-009-F remains NOT_RUN.
