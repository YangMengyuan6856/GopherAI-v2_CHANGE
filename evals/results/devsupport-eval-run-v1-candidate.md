# GopherAI Unified Evaluation Report

- Run: evalrun-7ee68856a4784f82
- Candidate: 5f9614a978b8
- Generated: 2026-09-06T04:58:01Z
- Review manifest SHA-256: 991756db933118fcfacccfb68ebca852394ce6dcd3ca477a11fe70a1dbdbf46d
- Decision: 技术候选
- Technical gates: true
- Human reviewed: false
- Baseline eligible: false

## Coverage

| Catalog | Validated | Executable | Completed | Catalog-only | Execution coverage |
|---:|---:|---:|---:|---:|---:|
| 320 | 320 | 300 | 300 | 20 | 93.75% |

## Slice scorecard

| Slice | Cases | Completion | Technical gate | Human reviewed | Passed |
|---|---:|---:|---:|---:|---:|
| intent | 150 | 100.00% | true | false | true |
| rag | 60 | 100.00% | true | false | true |
| diagnosis | 40 | 100.00% | true | false | true |
| tool | 30 | 100.00% | true | false | true |
| memory | 20 | 100.00% | true | false | true |

## Observed failure clusters

- `diagnosis/verification_gap`: 1 case(s)
- `intent/misclassification`: 6 case(s)
- `intent/severe_misroute`: 1 case(s)
- `rag/citation_gap`: 1 case(s)
