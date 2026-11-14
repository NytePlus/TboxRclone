# Small-file protocol baseline

23552-byte independently verified fixture;10 HEAD,5 sequential GET,8 GET with4 workers. HEAD p95=1.735s; sequential GET p95=2.421s; concurrent GET p95=4.962s. Small sample sizes do not establish stable percentile or large-file throughput. All13 GET bodies matched SHA-256. One HEAD returned404 after upstream directory403, so all_content_correct=false and this baseline must not be marked PASS. The error mapping has separate regression/live correction evidence in ST-015-F/read-metadata-status-20261114.

Complete service event sequence includes36 control-plane GET,1 POST and13 data-plane GET during the run. Ambient Finder PROPFIND occurred, so these are service-wide counts, not exact per-probe attribution. Directory caching is disabled for freshness. No CPU/RSS peak, large-file, injected network, multiple-distinct-file or Finder interaction acceptance is established here.
