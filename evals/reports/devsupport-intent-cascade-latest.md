# Intent Cascade Evaluation

- Dataset: devsupport-intent-v1 (150 cases)
- Rubric: intent-rubric-v1
- Recognizer: intent-cascade-v1
- Candidate: 2dbaf8184c77589e67fd0a02ea3769b2b8189b70
- Human reviewed: false
- Baseline eligible: false

| Metric | Result | Gate |
|---|---:|---:|
| Accuracy | 0.9533 | >= 0.92 |
| Macro-F1 | 0.9530 | >= 0.90 |
| Minimum class recall | 0.8800 | >= 0.85 |
| Severe misroute rate | 0.0067 | <= 0.03 |
| Prototype call rate | 0.5533 | observed |
| LLM call rate | 0.5533 | observed |
| Expected calibration error | 0.1925 | observed |
| P95 cascade latency | 878 ms | observed |
| Technical gate passed | true | all required gates |

The dataset is not a resume-grade baseline until every case is human reviewed.
A technical pass on pending labels remains a candidate result only.

## Per-label metrics

| Label | Support | Predicted | Precision | Recall | F1 |
|---|---:|---:|---:|---:|---:|
| `project_qa` | 25 | 27 | 0.9259 | 1.0000 | 0.9615 |
| `troubleshooting` | 25 | 26 | 0.9231 | 0.9600 | 0.9412 |
| `doc_task` | 25 | 25 | 0.9600 | 0.9600 | 0.9600 |
| `tool_task` | 25 | 23 | 0.9565 | 0.8800 | 0.9167 |
| `follow_up` | 25 | 25 | 1.0000 | 1.0000 | 1.0000 |
| `general` | 25 | 24 | 0.9583 | 0.9200 | 0.9388 |

## Cascade stage contribution

| Final stage | Cases | Correct | Accuracy |
|---|---:|---:|---:|
| `pattern` | 67 | 67 | 1.0000 |
| `llm` | 45 | 42 | 0.9333 |
| `degraded_clarification` | 38 | 34 | 0.8947 |

## Confidence calibration

| Bin | Cases | Average confidence | Accuracy |
|---|---:|---:|---:|
| [0.0, 0.6) | 38 | 0.5142 | 0.8947 |
| [0.6, 0.8) | 44 | 0.6677 | 0.9318 |
| [0.8, 0.9) | 1 | 0.8097 | 1.0000 |
| [0.9, 1.0] | 67 | 0.9610 | 1.0000 |

## Misclassifications (7)

| Case | Expected | Predicted | Confidence | Stage | Severe |
|---|---|---|---:|---|---:|
| `intent-v1-046` | `troubleshooting` | `tool_task` | 0.7900 | `llm` | false |
| `intent-v1-062` | `doc_task` | `troubleshooting` | 0.7000 | `llm` | false |
| `intent-v1-089` | `tool_task` | `doc_task` | 0.5600 | `degraded_clarification` | false |
| `intent-v1-095` | `tool_task` | `troubleshooting` | 0.6900 | `llm` | false |
| `intent-v1-097` | `tool_task` | `general` | 0.4200 | `degraded_clarification` | true |
| `intent-v1-133` | `general` | `project_qa` | 0.4300 | `degraded_clarification` | false |
| `intent-v1-138` | `general` | `project_qa` | 0.4200 | `degraded_clarification` | false |
