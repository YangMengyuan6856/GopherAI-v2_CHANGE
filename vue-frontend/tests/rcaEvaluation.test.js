import test from 'node:test'
import assert from 'node:assert/strict'
import { evaluationCases, summarizeAgentReport } from '../src/utils/rcaEvaluation.mjs'

test('only the six evaluation repetitions are selectable', () => {
  const catalog = [
    ...Array.from({ length: 6 }, (_, i) => ({ id: `r${i}`, split: 'reference' })),
    ...Array.from({ length: 6 }, (_, i) => ({ id: `d${i}`, split: 'development' })),
    ...Array.from({ length: 6 }, (_, i) => ({ id: `e${i}`, split: 'holdout' }))
  ]
  assert.deepEqual(evaluationCases(catalog).map(row => row.id), ['e0', 'e1', 'e2', 'e3', 'e4', 'e5'])
})

test('failed runs remain in the denominator and cannot inherit a score', () => {
  const report = summarizeAgentReport({
    metrics: { attempted: 999, joint_correct: 999 },
    cases: [
      { valid: true, evidence_valid: true, model_calls: 3, tool_calls: 2, input_tokens: 10, output_tokens: 5, elapsed_ms: 100, score: { service_top1: true, joint_correct: true } },
      { valid: false, evidence_valid: false, model_calls: 2, tool_calls: 1, input_tokens: 8, output_tokens: 2, elapsed_ms: 80, score: { service_top1: true, joint_correct: true } }
    ]
  })
  assert.deepEqual(report.metrics, {
    attempted: 2,
    completed: 1,
    execution_failed: 1,
    service_top1: 1,
    joint_correct: 1,
    evidence_valid: 1,
    model_calls: 5,
    tool_calls: 3,
    input_tokens: 18,
    output_tokens: 7,
    elapsed_ms_total: 180
  })
})
