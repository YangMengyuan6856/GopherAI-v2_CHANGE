// Presentation scope, not a change to the frozen dataset or diagnosis policy.
// Select by explicit case IDs, never by whether a model answered correctly.
export const demoCaseIDs = Object.freeze([
  'rca-34a5398b52', 'rca-0ae1ec693f', 'rca-ce14e33b0b',
  'rca-e5abfe8ebd', 'rca-e39c7bb9d6', 'rca-c91fd2df9f'
])
const retiredCaseIDs = new Set([
  'rca-bba0bc25ab', 'rca-90376d73d9', 'rca-a254fd4742',
  'rca-d1059aa9c3', 'rca-3a0809d8d0', 'rca-a28d1285e7'
])
export const isActiveCase = row => !retiredCaseIDs.has(row.id)
const isDemoCase = row => demoCaseIDs.includes(row.id)

export function projectAgentReport(report) {
  const cases = report.cases.filter(isDemoCase)
  const metrics = { attempted: cases.length, completed: 0, service_top1: 0, joint_correct: 0, execution_failed: 0, model_calls: 0 }
  for (const row of cases) {
    metrics.model_calls += row.model_calls || 0
    if (!row.valid) { metrics.execution_failed++; continue }
    metrics.completed++
    if (row.score.service_top1) metrics.service_top1++
    if (row.score.joint_correct) metrics.joint_correct++
  }
  return { ...report, cases, metrics }
}

export function projectRuleReport(report) {
  const cases = report.cases.filter(isDemoCase)
  const metrics = {}
  for (const strategy of ['legacy', 'feature_only', 'case_based']) {
    const rows = cases.filter(row => row.strategy === strategy)
    metrics[strategy] = {
      supported: rows.length,
      top1: rows.filter(row => !row.error && row.score.service_top1).length,
      joint: rows.filter(row => !row.error && row.score.joint_correct).length,
      in_scope_rejected: rows.filter(row => !row.error && row.score.rejected).length,
      execution_failed: rows.filter(row => row.error).length
    }
  }
  return { ...report, cases, metrics }
}
