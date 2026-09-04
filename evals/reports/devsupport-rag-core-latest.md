# DevSupport RAG Core Evaluation

- Technical metric status: **PASS**
- Dataset: devsupport-rag-core-v1 (20 cases)
- Fixture: kb-fixture-v1
- Candidate: d6add7fee7de0fd05fbe0ff267bfa8a54e46a6e3
- Generated at: 2026-09-04T08:07:08Z
- Dataset SHA-256: 1ac982b1bb61b6668a8444ab88d7640e265de8714a33786857e60e282683a0d2
- Fixture SHA-256: a01fdc45bfbd0f2cbb64e477ea61dbb01cc5198bae3e41c69bbbbf8a27a72bf0
- Retriever: hybrid-rrf-v1
- Embedding model: text-embedding-v4
- Chat model: qwen-turbo
- Environment: eval-rag-core-v1
- Per-case timeout: 90s
- External model behavior mutable: true
- Human label review complete: false
- Eligible to freeze as baseline: false

| Metric | Actual | Target |
|---|---:|---:|
| Recall@5 | 1.0000 | >= 0.85 |
| nDCG@5 | 0.9815 | >= 0.75 |
| MRR | 0.9750 | report |
| Citation Precision | 0.9545 | >= 0.90 |
| Citation Coverage | 0.9500 | >= 0.90 |
| Unauthorized Recall | 0 | = 0 |
| Resolved Answer Rate | 0.9500 | report |
| Error Rate | 0.0000 | = 0 |

Citation Coverage in this M3 core slice is a conservative evidence-reference proxy: a case passes only when the answer is resolved and every human-labelled relevant chunk is cited. Claim-level semantic coverage and LLM-as-a-Judge remain M8 scope.

| Case | Recall@5 | nDCG@5 | RR | Resolved | Citation covered | Error |
|---|---:|---:|---:|---|---|---|
| rag-core-001 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-002 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-003 | 1.0000 | 0.6309 | 0.5000 | true | true |  |
| rag-core-004 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-005 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-006 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-007 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-008 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-009 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-010 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-011 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-012 | 1.0000 | 1.0000 | 1.0000 | false | false |  |
| rag-core-013 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-014 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-015 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-016 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-017 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-018 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-019 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
| rag-core-020 | 1.0000 | 1.0000 | 1.0000 | true | true |  |
