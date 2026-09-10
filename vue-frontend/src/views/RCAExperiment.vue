<template>
  <div class="rca-page">
    <header class="hero panel">
      <div><small class="eyebrow">RCAEVAL / AUTONOMOUS DIAGNOSTIC AGENT</small><h1>自主排查与历史案例实验</h1><p>模型自主选择排查工具，根据每轮新证据更新假设，最终给出候选故障与待确认项。不是固定调用顺序，也不操作真实服务器。</p></div>
      <div class="boundary"><strong>只读 · 不修复</strong><span>2 个服务 / 3 类已知模式</span><span>单 Agent · 云端模型 · 有界执行</span></div>
    </header>

    <div v-if="error" role="alert" class="warning">{{ error }}</div>
    <section class="controls panel">
      <div class="control-row">
        <label>选择案例分组<select v-model="split" :disabled="busy" @change="changeSplit"><option value="holdout">已知故障回放 · 6 例（窗口 13–18）</option><option value="development">开发调试 · 9 例</option><option value="reference">历史参考 · 6 例（不计测试成绩）</option></select></label>
        <label class="case-select">选择一个故障案例<select v-model="selected" :disabled="busy" @change="loadObservation"><option v-for="item in visibleCases" :key="item.id" :value="item.id">{{ item.title }} · {{ item.id }}</option></select></label>
        <button class="primary" :disabled="busy || !observation" @click="run('autonomous')">{{ busy ? (loadingRecord ? '读取记录…' : `正在排查 · ${elapsed} 秒…`) : '启动自主排查 Agent' }}</button>
      </div>
      <p>操作：选一个案例 → 启动 Agent → 查看下方每轮证据和假设更新 → 最后展开标准答案核对。右侧服务选择只切换数据展示，不指定故障答案。</p>
      <p v-if="busy && !loadingRecord" role="status" class="accent">正在调用真实云端模型，最多 180 秒。完成后展示实际工具顺序；离开本页会取消本次请求。</p>
      <details><summary>规则对照（零模型调用，不是自主 Agent）</summary><div class="control-row"><button :disabled="busy || !observation" @click="run('case_based')">运行案例增强诊断（原规则版）</button><button :disabled="busy || !observation" @click="run('feature_only')">仅特征规则对照</button><button :disabled="busy || !observation" @click="run('legacy')">原文本规则对照</button></div></details>
      <p class="muted">本轮演示与下方统计仅覆盖窗口 13–18：checkoutservice、currencyservice 的 CPU 压力 / 内存压力 / 网络延迟。窗口 22–27 已退出本轮测试，历史数据与完整报告仍保留；本页不评估未知类型识别能力。开发调试和历史参考不计入下方统计。</p>
      <small v-if="catalog" class="mono">{{ catalog.agent_version }} · 数据 {{ catalog.dataset_sha256.slice(0, 16) }} · 最多 8 轮模型 / 6 次工具 / 单并发</small>
    </section>

    <section v-if="observation" class="panel">
      <div class="heading"><h2>01 / 当前观测，不含标准答案</h2><label>仅切换下方数据展示<select v-model="evidenceService" aria-label="查看哪个服务的证据"><option v-for="s in observation.services" :key="s.name" :value="s.name">{{ s.name }}</option></select></label></div>
      <p class="mono muted">{{ time(observation.start) }} → {{ time(observation.end) }}</p>
      <div class="stats"><article><strong>{{ count(observation.metric_rows) }}</strong><span>指标时间点</span></article><article><strong>{{ count(observation.log_rows) }}</strong><span>原始日志行</span></article><article><strong>{{ count(observation.trace_rows) }}</strong><span>调用链 span 记录</span></article><article><strong>1 / 3</strong><span>最早与最晚三分之一窗口比较</span></article></div>
      <div class="table-scroll"><table><thead><tr><th>指标（原始单位）</th><th>参考中位数</th><th>当前中位数</th><th>变化倍数</th><th>全窗口趋势 · 12 桶</th></tr></thead><tbody><tr v-for="m in metricRows" :key="m.id"><td>{{ m.column }}</td><td>{{ value(m.reference) }}</td><td>{{ value(m.current) }}</td><td :class="{ accent: m.ratio >= 2 }">{{ m.ratio.toFixed(2) }}×</td><td><svg viewBox="0 0 150 28" role="img" :aria-label="m.column + ' 全窗口趋势'"><polyline :points="spark(m.sparkline)" /></svg></td></tr></tbody></table></div>
      <details v-if="service"><summary>日志与调用链摘要</summary><div class="two-columns"><div><h3>日志</h3><p>当前 {{ service.logs.current_count || 0 }} 条；错误关键词 {{ service.logs.keyword_matches || 0 }} 条。</p><pre v-for="(line, i) in service.logs.examples || []" :key="i">{{ line.timestamp }} {{ line.message }}</pre></div><div><h3>调用链</h3><p>P95 原始值：{{ value(service.traces.reference_p95) }} → {{ value(service.traces.current_p95) }}</p><p>当前 {{ service.traces.current_spans || 0 }} 个 span，非零状态 {{ service.traces.nonzero_status_count || 0 }} 个（不直接等同于业务错误）。</p><code>样例 span：{{ service.traces.sample_span || '缺失' }}</code><p>{{ service.traces.sample_operation }}</p></div></div></details>
      <details><summary>数据来源与提取边界</summary><p v-for="w in observation.warnings" :key="w" class="muted">{{ w }}</p><p class="muted">故障来自公开演示系统的注入实验，并非当前 GopherAI 服务器发生了故障。当前只保留有界摘要；CPU 和延迟单位尚未从采集定义独立核实。</p><p v-for="source in observation.sources" :key="source.name" class="mono wrap">{{ source.name }} · SHA256 {{ source.sha256 }}</p></details>
    </section>

    <section v-if="result" class="panel result-panel" aria-live="polite">
      <p v-if="result.recorded" class="warning">正在查看已记录的真实模型轨迹（{{ result.recorded_at }}），不是本次新执行；下方耗时与用量属于记录当时。点击顶部“启动自主排查 Agent”才会重新调用模型。</p>
      <div class="heading"><h2>02 / 排查结果</h2><span :class="['badge', diagnosis.status === 'matched_hypothesis' ? 'matched' : 'limited']">{{ diagnosis.status === 'matched_hypothesis' ? '已匹配候选 · 待验证' : '证据不足 / 未覆盖' }}</span></div>
      <p class="result-summary">{{ diagnosis.summary }}</p>
      <p class="mono muted">{{ modeName(diagnosis.strategy) }} · {{ result.run.elapsed_ms.toFixed(2) }}ms · {{ result.run.tool_calls.length }} 次只读工具 · LLM {{ diagnosis.model_calls }} 次</p>
      <div v-if="result.run.agent" class="agent-trace">
        <div class="heading"><h3>实际排查过程 · 简短假设更新与证据记录</h3><button @click="downloadRun">下载本次轨迹 JSON</button></div>
        <p class="mono wrap">模型 {{ result.run.agent.model }} · 输入 / 输出 {{ result.run.agent.input_tokens }} / {{ result.run.agent.output_tokens }} tokens · 停止 {{ result.run.agent.stop_reason }}</p>
        <p v-if="!result.run.agent.completed" class="warning">本次未完成：不能把模型错误、预算用尽或超时算作正确拒答，也没有自动回退成规则答案。</p>
        <article v-for="step in result.run.agent.steps" :key="step.round" class="candidate">
          <div class="heading"><h3>第 {{ step.round }} 轮 · {{ step.decision?.action === 'finish' ? '提交结论' : '选择下一步' }}</h3><small class="mono">{{ step.model_ms }}ms · {{ step.validation }}</small></div>
          <template v-if="step.decision">
            <p>{{ step.decision.update }}</p>
            <ul><li v-for="(h, i) in step.decision.hypotheses" :key="i">{{ h.service }} / {{ faultName(h.fault) }} · {{ hypothesisName(h.status) }}<small class="evidence-ref">{{ (h.evidence_ids || []).join(' · ') }}</small></li></ul>
            <p v-if="step.decision.tool" class="accent">模型选择：{{ toolName(step.decision.tool.name) }} → {{ step.decision.tool.service }} · 执行 {{ step.tool_status }}</p>
          </template>
          <details v-if="step.observation?.length"><summary>本轮新返回 {{ step.observation.length }} 项证据（下一轮模型实际可见）</summary><pre v-for="e in step.observation" :key="e.id">{{ e.id }} · {{ e.service }}
{{ JSON.stringify(e.data, null, 2) }}</pre></details>
        </article>
        <p v-for="q in result.run.agent.questions" :key="q" class="amber">需要补充：{{ q }}</p>
      </div>
      <div v-if="diagnosis.candidates.length" class="candidate-list">
        <article v-for="(candidate, index) in diagnosis.candidates" :key="candidate.service + candidate.fault" class="candidate">
          <div class="heading"><h3><span class="accent">#{{ index + 1 }}</span> {{ candidate.service }} · {{ candidate.cause }}</h3><small v-if="!result.run.agent" class="mono">排序分 {{ candidate.score.toFixed(3) }}，非概率</small><small v-else class="muted">模型候选，非已确认根因</small></div>
          <div class="two-columns"><div><h4>当前依据</h4><ul><li v-for="e in candidate.evidence" :key="e.id">{{ e.statement }}<small class="evidence-ref">[{{ e.id }}]</small></li></ul></div><div><h4>{{ result.run.agent ? '不确定性与待确认边界' : '历史参考与差异' }}</h4><template v-if="candidate.reference_id || result.run.agent"><p v-if="candidate.reference_id" class="mono">{{ candidate.reference_id }} · 相似度 {{ candidate.similarity.toFixed(3) }}</p><ul><li v-for="difference in candidate.differences" :key="difference">{{ difference }}</li></ul><p class="muted">历史参考是否被模型使用，以实际工具轨迹和引用为准；相似不是根因证明。</p></template><p v-else class="muted">本策略不使用历史案例。</p></div></div>
          <h4>还需要确认什么 · 以下动作未执行</h4><div class="followups"><article v-for="step in candidate.follow_ups" :key="step.check"><strong>{{ step.check }}</strong><p><span class="accent">支持：</span>{{ step.supports }}</p><p><span class="amber">削弱：</span>{{ step.weakens }}</p></article></div>
        </article>
      </div>
      <details open><summary>本次真实工具轨迹</summary><div class="table-scroll"><table><thead><tr><th>工具</th><th>状态</th><th>耗时</th><th>证据引用数</th></tr></thead><tbody><tr v-for="tool in result.run.tool_calls" :key="tool.call_id"><td>{{ tool.tool_name }}</td><td>{{ tool.status }} {{ tool.degraded_reason || '' }}</td><td>{{ tool.latency_ms }}ms</td><td>{{ (tool.evidence_refs || []).length }}</td></tr></tbody></table></div><p class="mono wrap muted">Trace {{ result.run.trace_id }} · 审计 {{ result.run.audit_storage }}</p><p class="muted">只访问当前 benchmark 快照，不访问生产 SSH，不执行修复。自主 Agent 根据实际所选工具返回的信息判断；旧规则对照仅用指标排序。</p></details>
      <div class="answer-block"><button @click="reveal = !reveal">{{ reveal ? '收起标准答案核对' : '运行已结束，展开标准答案核对' }}</button><div v-if="reveal" class="answer-detail"><p>官方根因服务：<strong>{{ result.score.answer.service }}</strong>；故障类型：<strong>{{ faultName(result.score.answer.fault) }}</strong>。</p><p v-if="result.run.agent && !result.run.agent.completed" class="amber">本次执行未完成，不计为正确诊断或正确拒答。</p><p v-else-if="result.score.answer.supported">服务 Top-1：{{ result.score.service_top1 ? '正确' : '未命中' }}；服务与类型同时正确：{{ result.score.joint_correct ? '是' : '否' }}。</p><p v-else class="amber">该故障未纳入本版支持范围。{{ result.score.false_acceptance ? '本次仍匹配了已知模式：这是误接纳，应继续人工核查。' : '本次没有强行匹配，返回了证据不足。' }}</p><small class="muted">标准答案由运行结束后的独立评分器读取，不进入模型上下文。</small></div></div>
      <p v-for="w in diagnosis.warnings" :key="w" class="muted">{{ w }}</p>
    </section>

    <section class="panel">
      <div class="heading"><h2>03 / 自主 Agent 真实模型回放</h2><small class="mono">REAL MODEL / 非新盲测</small></div>
      <template v-if="agentReport">
        <p>以下从已保存的真实云端模型报告中取窗口 13–18，不重新运行、不使用旧规则成绩。模型：{{ agentReport.cases[0]?.model }}。保留这 6 例中的正确、错误与执行失败；重新运行单例可能产生不同路径和结论。</p>
        <div class="stats"><article><strong>{{ agentReport.metrics.completed }} / {{ agentReport.metrics.attempted }}</strong><span>已知案例执行完成（不等于答对）</span></article><article><strong>{{ agentReport.metrics.service_top1 }} / {{ agentReport.metrics.attempted }}</strong><span>已知案例：服务定位正确</span></article><article><strong>{{ agentReport.metrics.joint_correct }} / {{ agentReport.metrics.attempted }}</strong><span>已知案例：服务与类型均正确</span></article><article><strong>{{ agentReport.metrics.execution_failed }}</strong><span>本轮范围内执行失败</span></article></div>
        <p class="muted">仅这 {{ agentReport.metrics.attempted }} 例共 {{ agentReport.metrics.model_calls }} 次模型请求。范围缩减发生在历史回放之后，不是新盲测，也不代表整体准确率提升或具备未知故障识别能力。</p>
        <details><summary>逐例查看真实执行情况</summary><div class="table-scroll"><table><thead><tr><th>案例</th><th>是否完成</th><th>核对结果</th><th>模型轮数</th><th></th></tr></thead><tbody><tr v-for="row in agentReport.cases" :key="row.id"><td>{{ row.title }}</td><td>{{ row.valid ? '完成' : row.stop_reason }}</td><td>{{ !row.valid ? '执行失败' : row.score.answer.supported ? (row.score.joint_correct ? '服务/类型正确' : '未正确定位') : row.score.false_acceptance ? '误接纳' : '明确拒答' }}</td><td>{{ row.model_calls }}</td><td><button :disabled="busy" @click="viewRecorded(row.id)">查看记录轨迹</button> <button :disabled="busy" @click="openCase(row.id)">选择此例重新运行</button></td></tr></tbody></table></div></details>
      </template>
      <p v-else class="muted">{{ agentReportError || '正在读取自主 Agent 报告…' }}</p>
    </section>
    <section class="panel">
      <div class="heading"><h2>04 / 原规则版冻结评测（不是自主 Agent 成绩）</h2><small class="mono">RULE BASELINE / 零模型调用</small></div>
      <p class="muted">同样只展示窗口 13–18。标准答案此前已被查看，不能称为新的盲测，也不能借用下表 6/6 作为模型成绩。</p>
      <template v-if="report"><p class="muted">三种规则使用相同的 6 个已知故障窗口，保留该范围内的错误，不选择最好一次。这里的耗时来自本地离线执行，不代表 ECS 性能。</p><div class="table-scroll"><table><thead><tr><th>策略</th><th>服务 Top-1</th><th>服务+类型正确</th><th>范围内拒答</th><th>执行失败</th></tr></thead><tbody><tr v-for="strategy in strategies" :key="strategy"><td>{{ modeName(strategy) }}</td><td>{{ report.metrics[strategy].top1 }} / {{ report.metrics[strategy].supported }}</td><td>{{ report.metrics[strategy].joint }} / {{ report.metrics[strategy].supported }}</td><td>{{ report.metrics[strategy].in_scope_rejected }}</td><td>{{ report.metrics[strategy].execution_failed }}</td></tr></tbody></table></div><details><summary>逐例结果与失败案例</summary><div class="table-scroll"><table><thead><tr><th>窗口</th><th>官方标签</th><th>案例增强输出</th><th>结果</th><th></th></tr></thead><tbody><tr v-for="row in caseReportRows" :key="row.id"><td class="mono">{{ row.id }}</td><td>{{ row.score.answer.service }} / {{ faultName(row.score.answer.fault) }}</td><td>{{ row.candidates.length ? row.candidates[0].service + ' / ' + faultName(row.candidates[0].fault) : '证据不足' }}</td><td>{{ reportOutcome(row) }}</td><td><button :disabled="busy" @click="openCase(row.id)">重放</button></td></tr></tbody></table></div></details><p v-for="line in report.limitations" :key="line" class="muted">{{ line }}</p><small class="mono wrap">{{ report.matcher_version }} · {{ report.dataset_sha256 }}</small></template>
      <p v-else class="muted">{{ reportError || '正在读取报告…' }}</p>
    </section>
    <footer class="muted">数据来自 RCAEval / RE2-OB（MIT），已知模式范围内的辅助排查实验，不等同于生产根因确认或自动修复。<a href="https://huggingface.co/datasets/phamquiluan/RCAEval" target="_blank" rel="noopener noreferrer">查看来源</a></footer>
  </div>
</template>

<script>
import { computed, nextTick, onMounted, onBeforeUnmount, ref } from 'vue'
import api from '../utils/api'
import { isActiveCase, projectAgentReport, projectRuleReport } from '../utils/rcaDemoScope.mjs'

export default {
  name: 'RCAExperiment',
  setup() {
    const catalog = ref(null), report = ref(null), reportError = ref(''), error = ref('')
    const agentReport = ref(null), agentReportError = ref('')
    const split = ref('holdout'), selected = ref(''), evidenceService = ref('checkoutservice')
    const observation = ref(null), result = ref(null), reveal = ref(false), busy = ref(false), elapsed = ref(0)
    const loadingRecord = ref(false)
    let runTimer = null
    const controller = new AbortController()
    const options = { timeout: 190000, signal: controller.signal }
    // Axios adds /api; the existing gateway adds /v1 before forwarding to Gin.
    const endpoint = '/experiments/rca'
    const strategies = ['legacy', 'feature_only', 'case_based']
    const visibleCases = computed(() => (catalog.value?.cases || []).filter(c => c.split === split.value && isActiveCase(c)))
    const service = computed(() => observation.value?.services.find(s => s.name === evidenceService.value))
    const metricRows = computed(() => ['cpu', 'mem', 'latency-90', 'workload', 'socket'].map(k => service.value?.metrics[k]).filter(Boolean))
    const diagnosis = computed(() => result.value?.run.diagnosis)
    const caseReportRows = computed(() => (report.value?.cases || []).filter(row => row.strategy === 'case_based'))
    const time = stamp => new Date(stamp * 1000).toLocaleString('zh-CN', { timeZone: 'UTC', hour12: false }) + ' UTC'
    const count = n => Number(n || 0).toLocaleString('zh-CN')
    const value = n => Number.isFinite(n) ? Number(n.toPrecision(5)).toLocaleString('zh-CN') : '缺失'
    const faultName = key => ({ cpu: 'CPU 压力', mem: '内存压力', delay: '网络延迟', disk: '磁盘 I/O 压力', loss: '网络丢包', socket: 'Socket 故障' }[key] || key)
    const modeName = key => ({ legacy: 'A · 原文本规则', feature_only: 'B · 观测特征规则', case_based: 'C · 历史案例增强规则', autonomous: 'D · 自主排查 Agent' }[key] || key)
    const hypothesisName = key => ({ investigating: '待查假设', supported: '证据支持（待验证）', weakened: '被新证据削弱' }[key] || key)
    const toolName = key => ({ rca_inspect_overview: '查看服务总览', rca_inspect_metrics: '检查详细指标', rca_inspect_logs: '查询日志', rca_inspect_traces: '查询调用链', rca_inspect_history: '检索历史案例' }[key] || key)
    const downloadRun = () => {
      if (!result.value) return
      const url = URL.createObjectURL(new Blob([JSON.stringify(result.value, null, 2)], { type: 'application/json' }))
      const link = document.createElement('a'); link.href = url; link.download = `rca-${result.value.run.trace_id}.json`; link.click(); URL.revokeObjectURL(url)
    }
    const reportOutcome = row => row.score.answer.supported ? (row.score.joint_correct ? '服务/类型正确' : '未正确定位') : (row.score.false_acceptance ? '范围外误接纳' : row.error ? '执行失败' : '正确拒答')
    const spark = values => {
      if (!values?.length) return ''
      const lo = Math.min(...values), hi = Math.max(...values), scale = hi - lo || 1
      return values.map((v, i) => `${i * 146 / Math.max(1, values.length - 1) + 2},${26 - (v - lo) / scale * 24}`).join(' ')
    }
    const loadObservation = async () => {
      const id = selected.value
      if (!id) return
      result.value = null; reveal.value = false; observation.value = null; error.value = ''
      try { const response = await api.get(`${endpoint}/observations/${id}`, options); if (selected.value === id) observation.value = response.data } catch (e) { if (!controller.signal.aborted) error.value = e.response?.data?.message || '当前观测读取失败，请重试' }
    }
    const changeSplit = async () => { selected.value = visibleCases.value[0]?.id || ''; await loadObservation() }
    const openCase = async id => { split.value = 'holdout'; selected.value = id; await loadObservation(); window.requestAnimationFrame(() => document.querySelector('.rca-page')?.scrollTo({ top: 0, behavior: 'smooth' })) }
    const viewRecorded = async id => {
      if (busy.value) return
      busy.value = true; loadingRecord.value = true
      try {
        await openCase(id)
        if (!observation.value) return
        const response = await api.get(`${endpoint}/agent-report/${encodeURIComponent(id)}`, options)
        if (!response.data.recorded || !response.data.run) throw new Error('记录响应无效')
        result.value = response.data
        if (result.value.run.diagnosis.candidates.length) evidenceService.value = result.value.run.diagnosis.candidates[0].service
        await nextTick()
        document.querySelector('.result-panel')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      } catch (e) { if (!controller.signal.aborted) error.value = e.response?.data?.message || '记录读取失败' } finally { busy.value = false; loadingRecord.value = false }
    }
    const run = async strategy => {
      if (busy.value || !selected.value) return
      busy.value = true; error.value = ''; result.value = null; reveal.value = false
      elapsed.value = 0; runTimer = window.setInterval(() => { elapsed.value++ }, 1000)
      try {
        const response = await api.post(`${endpoint}/diagnose`, { case_id: selected.value, strategy }, options)
        if (!response.data.run) throw new Error('登录状态或诊断响应无效')
        result.value = response.data
        if (result.value.run.diagnosis.candidates.length) evidenceService.value = result.value.run.diagnosis.candidates[0].service
        await nextTick()
        document.querySelector('.result-panel')?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      } catch (e) { if (!controller.signal.aborted) error.value = e.response?.data?.message || e.message || '诊断失败' } finally { busy.value = false; window.clearInterval(runTimer) }
    }
    onMounted(async () => {
      api.get(`${endpoint}/agent-report`, options).then(r => { if (!r.data.metrics || !r.data.cases) throw new Error('报告无效'); agentReport.value = projectAgentReport(r.data) }).catch(e => { agentReportError.value = e.response?.data?.message || '自主 Agent 报告暂不可用，可直接运行单例' })
      api.get(`${endpoint}/report`, options).then(r => { if (!r.data.metrics || !r.data.cases) throw new Error('登录状态或报告响应无效'); report.value = projectRuleReport(r.data) }).catch(e => { reportError.value = e.response?.data?.message || '离线报告暂不可用' })
      try { const response = await api.get(endpoint, options); if (!response.data.cases) throw new Error('请重新登录后进入实验页'); catalog.value = response.data; await changeSplit() } catch (e) { if (!controller.signal.aborted) error.value = e.response?.data?.message || e.message }
    })
    onBeforeUnmount(() => { controller.abort(); window.clearInterval(runTimer) })
    return { catalog, report, reportError, agentReport, agentReportError, error, split, selected, evidenceService, observation, result, reveal, busy, elapsed, loadingRecord, visibleCases, service, metricRows, diagnosis, caseReportRows, strategies, time, count, value, faultName, modeName, hypothesisName, toolName, downloadRun, reportOutcome, spark, loadObservation, changeSplit, openCase, viewRecorded, run }
  }
}
</script>

<style scoped>
.rca-page{height:100%;overflow-y:auto;padding:24px clamp(14px,3vw,38px) 40px;background:var(--g-bg);color:var(--g-text-primary);font-size:14px;line-height:1.7}
.panel{background:var(--g-panel);border:1px solid var(--g-border);border-radius:2px;padding:22px;margin-bottom:18px;min-width:0}
.hero{display:flex;justify-content:space-between;gap:28px;border-top:2px solid var(--g-primary)}
.hero h1{font-size:28px;margin:8px 0}.hero p{max-width:860px;color:var(--g-text-secondary)}
.eyebrow,.mono{font-family:var(--g-font-mono);font-size:12px}.eyebrow,.accent{color:var(--g-primary)}
.boundary{display:grid;align-content:center;gap:7px;min-width:240px;padding-left:24px;border-left:1px solid var(--g-border);font-size:12px;color:var(--g-text-secondary)}.boundary strong{color:var(--g-success);font-size:17px}
.control-row{display:flex;flex-wrap:wrap;align-items:flex-end;gap:12px}label{display:grid;gap:6px;color:var(--g-text-secondary);font-size:12px}.case-select{flex:1;min-width:250px}
select,button{background:#101b25;color:var(--g-text-primary);border:1px solid var(--g-border-strong);border-radius:2px;min-height:38px;padding:7px 12px;font:inherit;max-width:100%}button{cursor:pointer}button:hover,select:focus{border-color:var(--g-primary)}button:disabled,select:disabled{opacity:.45;cursor:not-allowed}button.primary{color:#061216;background:var(--g-primary);font-weight:700}button:focus-visible,summary:focus-visible{outline:2px solid var(--g-primary);outline-offset:3px}
.heading{display:flex;justify-content:space-between;align-items:center;gap:15px;flex-wrap:wrap;margin-bottom:12px}h2{font-size:19px;margin:0}h3{font-size:16px;margin:4px 0}h4{margin:12px 0 8px;font-size:14px}.muted{color:var(--g-text-secondary);font-size:12px}.wrap{overflow-wrap:anywhere}.stats{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px;margin:18px 0}.stats article{display:grid;gap:4px;padding:12px;border:1px solid var(--g-border);background:#101b25}.stats strong{font:22px var(--g-font-mono);color:var(--g-primary)}.stats span{font-size:12px;color:var(--g-text-secondary)}
.table-scroll{overflow-x:auto}table{width:100%;border-collapse:collapse;text-align:left;font-size:12px}th{background:#101b25;color:var(--g-text-secondary);font-weight:500}td,th{padding:10px 12px;border-bottom:1px solid var(--g-border);white-space:nowrap}td{font-family:var(--g-font-mono)}svg{width:150px;height:28px;display:block}polyline{fill:none;stroke:var(--g-primary);stroke-width:1.8}
details{margin-top:16px;border-top:1px solid var(--g-border);padding-top:12px}summary{cursor:pointer;color:var(--g-primary);font-size:13px}.two-columns{display:grid;grid-template-columns:1fr 1fr;gap:24px}.two-columns>div{min-width:0}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#0b141c;padding:10px;border:1px solid var(--g-border);font:12px var(--g-font-mono)}code{font-family:var(--g-font-mono);overflow-wrap:anywhere}ul{padding-left:19px;margin:0}li{margin-bottom:9px;font-size:13px;color:var(--g-text-secondary)}.evidence-ref{display:block;font-family:var(--g-font-mono);overflow-wrap:anywhere;color:var(--g-text-muted);font-size:10px}
.result-panel{border-left:2px solid var(--g-primary)}.result-summary{font-size:17px}.badge{padding:3px 10px;border:1px solid var(--g-border);font-size:12px;background:#112b30}.matched{color:var(--g-success)}.limited,.amber{color:#e4b55d}.candidate{margin-top:16px;padding:18px;background:#101b25;border:1px solid var(--g-border)}.followups{display:grid;grid-template-columns:1fr 1fr;gap:12px}.followups article{padding:12px;border:1px solid var(--g-border);background:#0d1720}.followups p{font-size:12px;color:var(--g-text-secondary);margin:6px 0}.warning{padding:14px;border:1px solid #715528;color:#e4b55d;background:#282216;margin-bottom:16px}.answer-block{margin-top:18px}.answer-detail{background:#14252b;border:1px solid var(--g-border-strong);padding:14px;margin-top:12px}footer a{color:var(--g-primary)}
@media(max-width:950px){.hero{flex-direction:column}.boundary{border-left:0;padding:12px 0 0;border-top:1px solid var(--g-border)}.two-columns,.followups{grid-template-columns:1fr}.stats{grid-template-columns:repeat(2,minmax(0,1fr))}.panel{padding:16px}.hero h1{font-size:23px}}
</style>
