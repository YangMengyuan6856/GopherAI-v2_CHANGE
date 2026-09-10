import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { demoCaseIDs, isActiveCase, projectAgentReport, projectRuleReport } from '../src/utils/rcaDemoScope.mjs'

const read = path => JSON.parse(readFileSync(new URL(path, import.meta.url), 'utf8'))
const catalog = read('../../internal/rcaexperiment/data/observations.json').catalog
const frozenAgent = read('../../evals/rcaeval/agent-replay.json')
const frozenRules = read('../../evals/rcaeval/holdout.json')
// API returns condensed rows; use the same fields as the real controller.
const report = { ...frozenAgent, cases: frozenAgent.cases.map(row => ({ ...row, valid: row.evaluation_valid, model_calls: row.run.diagnosis.model_calls })) }

test('remove exactly windows 22–27 from selectable cases, preserve other groups', () => {
  const removed = catalog.filter(row => !isActiveCase(row))
  assert.deepEqual(removed.map(row => row.title), [22, 23, 24, 25, 26, 27].map(n => `观测窗口 ${n}`))
  assert.deepEqual(catalog.filter(row => row.split === 'holdout' && isActiveCase(row)).map(row => row.id), demoCaseIDs)
  assert.equal(catalog.filter(row => row.split === 'development' && isActiveCase(row)).length, 9)
  assert.equal(catalog.filter(row => row.split === 'reference' && isActiveCase(row)).length, 6)
})

test('recompute six-case metrics without changing the archived 12-case report', () => {
  const original = JSON.stringify(report)
  const projected = projectAgentReport(report)
  assert.deepEqual(projected.cases.map(row => row.id), demoCaseIDs)
  assert.equal(projected.metrics.attempted, 6)
  assert.equal(projected.metrics.completed, 6)
  assert.equal(projected.metrics.service_top1, 6)
  assert.equal(projected.metrics.joint_correct, 4)
  assert.equal(projected.metrics.execution_failed, 0)
  assert.equal(projected.metrics.model_calls, projected.cases.reduce((sum, row) => sum + row.model_calls, 0))
  assert.equal(projected.cases.filter(row => !row.score.joint_correct).length, 2)
  assert.equal(JSON.stringify(report), original)
  assert.equal(report.cases.length, 12)
})

test('a selected case that fails stays in the denominator, never counts as correct', () => {
  const first = report.cases[0]
  const failed = projectAgentReport({ ...report, cases: [{ ...first, valid: false }, ...report.cases.slice(1)] })
  assert.equal(failed.metrics.attempted, 6)
  assert.equal(failed.metrics.completed, 5)
  assert.equal(failed.metrics.execution_failed, 1)
  assert.equal(failed.metrics.joint_correct, 3)
})

test('rule comparison uses the same six windows and keeps incorrect results', () => {
  const original = JSON.stringify(frozenRules)
  const projected = projectRuleReport(frozenRules)
  assert.equal(projected.cases.length, 18)
  for (const m of Object.values(projected.metrics)) assert.equal(m.supported, 6)
  assert.equal(projected.metrics.legacy.joint, 0)
  assert.equal(projected.metrics.feature_only.joint, 5)
  assert.equal(projected.metrics.case_based.joint, 6)
  assert.equal(JSON.stringify(frozenRules), original)
})
