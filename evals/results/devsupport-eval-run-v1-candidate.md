# GopherAI Unified Evaluation Report

- Run: evalrun-fbdcff3b4b14d611
- Candidate: 5f9614a978b8
- Generated: 2026-09-05T09:26:40Z
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
