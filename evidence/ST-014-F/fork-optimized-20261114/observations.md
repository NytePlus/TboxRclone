# Native performance retest: not accepted

The deployed service command confirms `--dir-cache-time 5s`; original five pending records and payloads remain byte-identical after restart. Fixture content and resource forks match the baseline (fixture-comparison.json).

Native drag started 2026-11-14T08:43:29Z. At 08:45:05Z a read-only cloud metadata request failed with transport EOF during the directory AppleDouble update. Operation b3f7cf0f1b0187e459cb96639e686be4 remains Prepared (4096 bytes); source and durable payload must be retained. Later Finder retries were rejected because that path is pending. Do not count this as a clean successful timing comparison or delete the record.

Metadata cache probe: serial HEAD median 0.002030s, p95 0.366121s; all serial HEAD/GET content checks passed. Different baseline file and ambient Finder traffic mean no claimed whole-upload speedup. Protocol report is evidence/ST-018-T/metadata-cache-20261114/result.json.

A bounded retry for read-only transport failures is being validated separately. Mutations, explicit HTTP errors and protocol failures remain unreplayed.
