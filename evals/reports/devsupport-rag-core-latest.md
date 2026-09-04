# DevSupport RAG Core Evaluation

- Technical metric status: **PASS**
- Dataset: devsupport-rag-core-v2 (60 cases)
- Positive / no-evidence cases: 50 / 10
- Fixture: kb-fixture-v2
- Candidate: 5acfde1d4ba18e8840c866933d56acccf5660a17
- Generated at: 2026-09-04T11:01:32Z
- Dataset SHA-256: c4882a694401f52b4acb002614c0e7a4f4445740cf38b2fb0c50953798376104
- Fixture SHA-256: 14c27846dcaf3f8f962f51de60936302930bd36a81c2a8c0918054889189345e
- Retriever: hybrid-rrf-v1
- Embedding model: text-embedding-v4
- Chat model: qwen-turbo
- Environment: eval-rag-core-v2
- Per-case timeout: 90s
- External model behavior mutable: true
- Human label review complete: false
- Eligible to freeze as baseline: false

| Metric | Actual | Target |
|---|---:|---:|
| Recall@5 | 1.0000 | >= 0.85 |
| nDCG@5 | 0.9599 | >= 0.75 |
| MRR | 0.9500 | report |
| Citation Precision | 1.0000 | >= 0.90 |
| Citation Coverage | 0.9800 | >= 0.90 |
| Unauthorized Recall | 0 | = 0 |
| Resolved Answer Rate | 1.0000 | report |
| Evidence Gate Precision | 1.0000 | >= 0.85 |
| No-evidence Safe Rate | 1.0000 | >= 0.90 |
| Unsupported Answer Rate | 0.0000 | <= 0.05 |
| Citation Safety Fallback Rate | 0.0000 | report |
| Error Rate | 0.0000 | = 0 |
| Search P95 | 149 ms | report |
| Answer P95 | 1277 ms | report |
| End-to-end P95 | 1380 ms | <= 8000 ms (G3 observation) |

Citation Coverage in this M3 core slice is a conservative evidence-reference proxy: a case passes only when the answer is resolved and every human-labelled relevant chunk is cited. Claim-level semantic coverage and LLM-as-a-Judge remain M8 scope.

| Case | Recall@5 | nDCG@5 | RR | Resolved | Citation covered | Error |
|---|---:|---:|---:|---|---|---|
| rag-v2-001 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-002 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-003 | 1.0000 | 0.6309 | 0.5000 | true | true |  |
| rag-v2-004 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-005 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-006 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-007 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-008 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-009 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-010 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-011 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-012 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-013 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-014 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-015 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-016 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-017 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-018 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-019 | 1.0000 | 0.9197 | 1.0000 | true | true |  |
| rag-v2-020 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-021 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-022 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-023 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-024 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-025 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-026 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-027 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-028 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-029 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-030 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-031 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-032 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-033 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-034 | 1.0000 | 0.6309 | 0.5000 | true | true |  |
| rag-v2-035 | 1.0000 | 0.6309 | 0.5000 | true | true |  |
| rag-v2-036 | 1.0000 | 0.6309 | 0.5000 | true | true |  |
| rag-v2-037 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-038 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-039 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-040 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-041 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-042 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-043 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-044 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-045 | 1.0000 | 0.9197 | 1.0000 | true | false |  |
| rag-v2-046 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-047 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-048 | 1.0000 | 0.6309 | 0.5000 | true | true |  |
| rag-v2-049 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-050 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-v2-051 | 0.0000 | 0.0000 | 0.0000 | false | false |  |
| rag-v2-052 | 0.0000 | 0.0000 | 0.0000 | false | false |  |
| rag-v2-053 | 0.0000 | 0.0000 | 0.0000 | false | false |  |
| rag-v2-054 | 0.0000 | 0.0000 | 0.0000 | false | false |  |
| rag-v2-055 | 0.0000 | 0.0000 | 0.0000 | false | false |  |
| rag-v2-056 | 0.0000 | 0.0000 | 0.0000 | false | false |  |
| rag-v2-057 | 0.0000 | 0.0000 | 0.0000 | false | false |  |
| rag-v2-058 | 0.0000 | 0.0000 | 0.0000 | false | false |  |
| rag-v2-059 | 0.0000 | 0.0000 | 0.0000 | false | false |  |
| rag-v2-060 | 0.0000 | 0.0000 | 0.0000 | false | false |  |
