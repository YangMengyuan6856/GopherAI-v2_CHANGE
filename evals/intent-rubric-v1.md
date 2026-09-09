# GopherAI Intent Rubric v1

Version: `intent-rubric-v1`

This rubric freezes the six mutually exclusive primary labels used by the M4
evaluation. A request may carry secondary candidates and `is_compound=true`,
but every evaluation row has exactly one primary label selected by the rules
below.

| Label | Include | Exclude / boundary | Primary-label tie break |
|---|---|---|---|
| `project_qa` | A factual or procedural answer must be grounded in project documents, code, configuration, or release evidence. | Managing the document lifecycle is `doc_task`; diagnosing a reported failure is `troubleshooting`. | Prefer `troubleshooting` when the user supplies a symptom/error and asks why or how to fix it. |
| `troubleshooting` | The user supplies an error, log, symptom, failed expectation, or environment observation and wants cause isolation or verification steps. | A read-only request for current external state without a reported symptom is `tool_task`. | Safety priority over every other label because sending incidents to general/QA is severe. |
| `doc_task` | Upload, delete, rebuild, re-index, version, or inspect knowledge-document lifecycle state. | Asking what a document says is `project_qa`. | If the lifecycle operation itself failed and diagnosis is requested, use `troubleshooting`; otherwise `doc_task`. |
| `tool_task` | An explicit request to perform an external action or observation through a tool boundary, including a forbidden, unavailable, write or dangerous action. | Questions answerable from indexed project evidence are `project_qa`. Classification never grants execution permission: the tool-governance layer independently authorizes, confirms or refuses the request. | A reported failure plus root-cause request is `troubleshooting`; document lifecycle status is `doc_task`; otherwise an explicit external action remains `tool_task` even when execution must be refused. |
| `follow_up` | A turn depends on an earlier intent/result and is not independently resolvable, for example “那第二种怎么验证？”. | A self-contained new question is classified by its own content. | Requires session context. Without a valid previous primary intent it must not short-circuit the cascade. |
| `general` | Greetings, casual interaction, or a self-contained request that does not require project evidence, diagnosis, document management, or governed tools. | “Unknown” is not permission to bypass evidence/tool governance. | Use only after higher-risk signals are absent or the cascade explicitly asks for clarification. |

## Compound requests

Annotate `is_compound=true` only when two independently actionable tasks could
be executed or answered separately. Asking for several fields or services in
one observation, comparing two versions, checking the result of an upload, or
requesting diagnosis plus its recommended next step is one task and therefore
not compound. The primary label follows this priority for the v1 evaluator:
`troubleshooting > doc_task > tool_task > project_qa > follow_up > general`.
The priority controls the label only; it does not authorize execution. Pattern
conflicts must continue to the semantic/LLM fusion stages.

## Context and refusal boundaries

- A `follow_up` fixture must include the actual previous user turn and previous
  assistant result, not merely `previous_intent`. This makes pronouns, ordinals
  and commands such as “继续” independently reviewable.
- A contextual command remains `follow_up` when its object and meaning cannot be
  resolved without the previous result. Classification is separate from the
  downstream authorization decision.
- Document upload, version, indexing and lifecycle status are `doc_task` even
  when the implementation uses a tool. The user-facing operation determines the
  intent; the implementation mechanism does not.
- Requests to fabricate tool output, reveal secrets, execute arbitrary Shell,
  delete data, restart a service or change production policy are classified as
  `tool_task`, then refused by tool governance. A refusal is not a relabel to
  `general`, and a label is not permission to execute.

## Severe misroutes

- `troubleshooting → general|project_qa`: an operational incident loses the
  diagnostic contract.
- `project_qa → general`: the answer can bypass the evidence gate.
- `tool_task → general|project_qa`: the answer can bypass tool governance or
  fabricate external state.

The G4 severe-misroute target is at most 3%. Document-management and follow-up
confusions remain ordinary errors unless they also produce one of the unsafe
transitions above.

## Review protocol

Each JSONL row carries `reviewed_by`. Keep it `pending_user` until the question,
context, primary label, compound flag, and severe alternatives are manually
reviewed. Reports generated before that point must set `human_reviewed=false`
and `baseline_eligible=false` even if all numerical targets pass.
