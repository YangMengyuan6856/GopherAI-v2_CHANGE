# Intent Pattern Evaluation

- Dataset: devsupport-intent-v1 (150 cases)
- Rubric: intent-rubric-v1
- Recognizer: intent-pattern-v1
- Candidate: 8dd517ad3610f97076cc5538a0cc829d6b674e6f
- Human reviewed: false
- Baseline eligible: false

## Pattern-stage metrics

| Metric | Result |
|---|---:|
| Coverage | 0.4467 |
| Selective accuracy | 1.0000 |
| False short-circuit rate | 0.0000 |
| Severe short-circuit rate | 0.0000 |
| Conflicts passed to later stages | 6 |
| P95 pattern latency | 1 µs |
| Pattern gate passed | true |
| Full G4 evaluated | false |

This is a selective stage report, not end-to-end intent accuracy. Abstentions
must proceed to Prototype/LLM fusion; counting them as general would hide
missing coverage. G4 remains unevaluated until the complete cascade exists.

## Per-label coverage

| Label | Support | Matched | Correct | Coverage | Correct / support |
|---|---:|---:|---:|---:|---:|
| `project_qa` | 25 | 9 | 9 | 0.3600 | 0.3600 |
| `troubleshooting` | 25 | 13 | 13 | 0.5200 | 0.5200 |
| `doc_task` | 25 | 9 | 9 | 0.3600 | 0.3600 |
| `tool_task` | 25 | 7 | 7 | 0.2800 | 0.2800 |
| `follow_up` | 25 | 21 | 21 | 0.8400 | 0.8400 |
| `general` | 25 | 8 | 8 | 0.3200 | 0.3200 |
