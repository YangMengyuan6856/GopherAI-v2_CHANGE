# Profile Memory Selection Rules v1

Version: `profile-memory-selection-v1`

This document freezes the deterministic rules used by the 20 synthetic memory
cases. It is part of the evaluation evidence envelope, not a description of a
user's real environment.

1. Scope gate: a candidate must belong to the current tenant and user. Facts
   from `other_user` or `other_tenant` are never eligible.
2. State gate: only `active` facts are eligible. Deleted IDs, `candidate` and
   `conflicted` facts are excluded.
3. Freshness gate: an expired fact is excluded. `fresh` and the intentionally
   non-expiring value `none` may continue.
4. Confidence gate: confidence must be greater than or equal to `0.80`.
5. Relevance gate: only keys directly requested by the query are eligible.
   “运行环境/部署信息” may map to `os`, `go_version`, `deployment_mode`,
   `cloud_provider`, `redis_version` and `mysql_version`. A cloud provider is
   not treated as a compilation environment.
6. Ranking: higher relevance score sorts first. Equal relevance sorts by
   `observed_order` descending (newest first), then key and stable ID ascending.
7. Same-key deduplication: after sorting, keep only the newest eligible fact for
   each key.
8. Budget: stop at the smaller of the requested `limit` and the context token
   budget. Items after the cut are forbidden from entering the prompt.

These rules make selection reproducible: the expected result must be derivable
from the case input without reading the expected JSON itself.
