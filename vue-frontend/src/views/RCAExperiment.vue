<template>
  <div class="rca-page">
    <header class="hero panel">
      <div><small class="eyebrow">RCAEVAL / KNOWN-FAULT CASE REPLAY</small><h1>历史案例辅助排查实验</h1><p>给定一个观测窗口，从已覆盖的故障模式中寻找候选；展示当前依据、历史相似点与仍需确认的内容。</p></div>
      <div class="boundary"><strong>只读 · 不修复</strong><span>2 个服务 / 3 类模式</span><span>本版确定性匹配 · LLM 调用 0</span></div>
    </header>

    <div v-if="error" role="alert" class="warning">{{ error }}</div>
    <section class="controls panel">
      <div class="control-row">
        <label>样本分组<select v-model="split" :disabled="busy" @change="changeSplit"><option value="holdout">留出实验 · 12 例</option><option value="development">开发调试 · 9 例</option><option value="reference">参考案例 · 6 例（不计测试成绩）</option></select></label>
        <label class="case-select">观测窗口<select v-model="selected" :disabled="busy" @change="loadObservation"><option v-for="item in visibleCases" :key="item.id" :value="item.id">{{ item.title }} · {{ item.id }}</option></select></label>
        <button class="primary" :disabled="busy || !observation" @click="run('case_based')">{{ busy ? '正在读取与匹配…' : '运行案例增强诊断' }}</button>
        <button :disabled="busy || !observation" @click="run('feature_only')">仅特征规则对照</button>
        <button :disabled="busy || !observation" @click="run('legacy')">原文本规则对照</button>
      </div>
      <p class="muted">支持 checkoutservice、currencyservice 的 CPU 压力 / 内存压力 / 网络延迟；其他类型可能拒答，也可能误匹配，报告保留所有结果。参考样本与测试样本来自不同实验运行。</p>
      <small v-if="catalog" class="mono">{{ catalog.matcher_version }} · 数据 {{ catalog.dataset_sha256.slice(0, 16) }} · 最多 4 次工具调用 / 单并发</small>
    </section>

    <section v-if="observation" class="panel">
      <div class="heading"><h2>01 / 当前观测，不含标准答案</h2><select v-model="evidenceService" aria-label="查看哪个服务的证据"><option v-for="s in observation.services" :key="s.name" :value="s.name">{{ s.name }}</option></select></div>
      <p class="mono muted">{{ time(observation.start) }} → {{ time(observation.end) }}</p>
      <div class="stats"><article><strong>{{ count(observation.metric_rows) }}</strong><span>指标时间点</span></article><article><strong>{{ count(observation.log_rows) }}</strong><span>原始日志行</span></article><article><strong>{{ count(observation.trace_rows) }}</strong><span>调用链 span 记录</span></article><article><strong>1 / 3</strong><span>最早与最晚三分之一窗口比较</span></article></div>
      <div class="table-scroll"><table><thead><tr><th>指标（原始单位）</th><th>参考中位数</th><th>当前中位数</th><th>变化倍数</th><th>全窗口趋势 · 12 桶</th></tr></thead><tbody><tr v-for="m in metricRows" :key="m.id"><td>{{ m.column }}</td><td>{{ value(m.reference) }}</td><td>{{ value(m.current) }}</td><td :class="{ accent: m.ratio >= 2 }">{{ m.ratio.toFixed(2) }}×</td><td><svg viewBox="0 0 150 28" role="img" :aria-label="m.column + ' 全窗口趋势'"><polyline :points="spark(m.sparkline)" /></svg></td></tr></tbody></table></div>
      <details v-if="service"><summary>日志与调用链摘要</summary><div class="two-columns"><div><h3>日志</h3><p>当前 {{ service.logs.current_count || 0 }} 条；错误关键词 {{ service.logs.keyword_matches || 0 }} 条。</p><pre v-for="(line, i) in service.logs.examples || []" :key="i">{{ line.timestamp }} {{ line.message }}</pre></div><div><h3>调用链</h3><p>P95 原始值：{{ value(service.traces.reference_p95) }} → {{ value(service.traces.current_p95) }}</p><p>当前 {{ service.traces.current_spans || 0 }} 个 span，非零状态 {{ service.traces.nonzero_status_count || 0 }} 个（不直接等同于业务错误）。</p><code>样例 span：{{ service.traces.sample_span || '缺失' }}</code><p>{{ service.traces.sample_operation }}</p></div></div></details>
      <details><summary>数据来源与提取边界</summary><p v-for="w in observation.warnings" :key="w" class="muted">{{ w }}</p><p class="muted">故障来自公开演示系统的注入实验，并非当前 GopherAI 服务器发生了故障。当前只保留有界摘要；CPU 和延迟单位尚未从采集定义独立核实。</p><p v-for="source in observation.sources" :key="source.name" class="mono wrap">{{ source.name }} · SHA256 {{ source.sha256 }}</p></details>
    </section>

    <section v-if="result" class="panel result-panel" aria-live="polite">
      <div class="heading"><h2>02 / 排查结果</h2><span :class="['badge', diagnosis.status === 'matched_hypothesis' ? 'matched' : 'limited']">{{ diagnosis.status === 'matched_hypothesis' ? '已匹配候选 · 待验证' : '证据不足 / 未覆盖' }}</span></div>
      <p class="result-summary">{{ diagnosis.summary }}</p>
      <p class="mono muted">{{ modeName(diagnosis.strategy) }} · {{ result.run.elapsed_ms.toFixed(2) }}ms · {{ result.run.tool_calls.length }} 次只读工具 · LLM 0 次</p>
      <div v-if="diagnosis.candidates.length" class="candidate-list">
        <article v-for="(candidate, index) in diagnosis.candidates" :key="candidate.service + candidate.fault" class="candidate">
          <div class="heading"><h3><span class="accent">#{{ index + 1 }}</span> {{ candidate.service }} · {{ candidate.cause }}</h3><small class="mono">排序分 {{ candidate.score.toFixed(3) }}，非概率</small></div>
          <div class="two-columns"><div><h4>当前依据</h4><ul><li v-for="e in candidate.evidence" :key="e.id">{{ e.statement }}<small class="evidence-ref">[{{ e.id }}]</small></li></ul></div><div><h4>历史参考与差异</h4><template v-if="candidate.reference_id"><p class="mono">{{ candidate.reference_id }} · 相似度 {{ candidate.similarity.toFixed(3) }}</p><ul><li v-for="difference in candidate.differences" :key="difference">{{ difference }}</li></ul><p class="muted">相似案例提供先验，不是当前根因证明。历史修复结果未知。</p></template><p v-else class="muted">本策略不使用历史案例。</p></div></div>
          <h4>还需要确认什么 · 以下动作未执行</h4><div class="followups"><article v-for="step in candidate.follow_ups" :key="step.check"><strong>{{ step.check }}</strong><p><span class="accent">支持：</span>{{ step.supports }}</p><p><span class="amber">削弱：</span>{{ step.weakens }}</p></article></div>
        </article>
      </div>
      <details open><summary>本次真实工具轨迹</summary><div class="table-scroll"><table><thead><tr><th>工具</th><th>状态</th><th>耗时</th><th>证据引用数</th></tr></thead><tbody><tr v-for="tool in result.run.tool_calls" :key="tool.call_id"><td>{{ tool.tool_name }}</td><td>{{ tool.status }} {{ tool.degraded_reason || '' }}</td><td>{{ tool.latency_ms }}ms</td><td>{{ (tool.evidence_refs || []).length }}</td></tr></tbody></table></div><p class="mono wrap muted">Trace {{ result.run.trace_id }} · 审计 {{ result.run.audit_storage }}</p><p class="muted">只访问当前 benchmark 快照，不访问生产 SSH，不执行修复。日志/调用链作为补充观测展示，首版排序主要来自指标模式。</p></details>
      <div class="answer-block"><button @click="reveal = !reveal">{{ reveal ? '收起标准答案核对' : '诊断已完成，展开标准答案核对' }}</button><div v-if="reveal" class="answer-detail"><p>官方根因服务：<strong>{{ result.score.answer.service }}</strong>；故障类型：<strong>{{ faultName(result.score.answer.fault) }}</strong>。</p><p v-if="result.score.answer.supported">服务 Top-1：{{ result.score.service_top1 ? '正确' : '未命中' }}；服务与类型同时正确：{{ result.score.joint_correct ? '是' : '否' }}。</p><p v-else class="amber">该故障未纳入本版支持范围。{{ result.score.false_acceptance ? '本次仍匹配了已知模式：这是误接纳，应继续人工核查。' : '本次没有强行匹配，返回了证据不足。' }}</p><small class="muted">标准答案由诊断结束后的独立评分器读取，不参与匹配计算。</small></div></div>
      <p v-for="w in diagnosis.warnings" :key="w" class="muted">{{ w }}</p>
    </section>

    <section class="panel">
      <div class="heading"><h2>03 / 冻结留出评测 · 同配方不同运行</h2><small class="mono">OFFLINE REPORT / 非实时故障检测</small></div>
      <template v-if="report"><p class="muted">6 个范围内案例 + 6 个范围外案例。原始规则、观测规则与案例增强使用相同遥测，保留失败与误匹配，不选择最好一次。这里的耗时来自本地离线执行，不代表 ECS 性能。</p><div class="table-scroll"><table><thead><tr><th>策略</th><th>服务 Top-1</th><th>服务+类型正确</th><th>范围内拒答</th><th>范围外拒答</th><th>范围外误接纳</th></tr></thead><tbody><tr v-for="strategy in strategies" :key="strategy"><td>{{ modeName(strategy) }}</td><td>{{ report.metrics[strategy].top1 }} / {{ report.metrics[strategy].supported }}</td><td>{{ report.metrics[strategy].joint }} / {{ report.metrics[strategy].supported }}</td><td>{{ report.metrics[strategy].in_scope_rejected }}</td><td>{{ report.metrics[strategy].unknown_rejected }} / {{ report.metrics[strategy].unsupported }}</td><td class="amber">{{ report.metrics[strategy].false_acceptance }} / {{ report.metrics[strategy].unsupported }}</td></tr></tbody></table></div><details><summary>逐例结果与失败案例</summary><div class="table-scroll"><table><thead><tr><th>窗口</th><th>官方标签</th><th>案例增强输出</th><th>结果</th><th></th></tr></thead><tbody><tr v-for="row in caseReportRows" :key="row.id"><td class="mono">{{ row.id }}</td><td>{{ row.score.answer.service }} / {{ faultName(row.score.answer.fault) }}</td><td>{{ row.candidates.length ? row.candidates[0].service + ' / ' + faultName(row.candidates[0].fault) : '证据不足' }}</td><td>{{ reportOutcome(row) }}</td><td><button :disabled="busy" @click="openCase(row.id)">重放</button></td></tr></tbody></table></div></details><p v-for="line in report.limitations" :key="line" class="muted">{{ line }}</p><small class="mono wrap">{{ report.matcher_version }} · {{ report.dataset_sha256 }}</small></template>
      <p v-else class="muted">{{ reportError || '正在读取报告…' }}</p>
    </section>
    <footer class="muted">数据来自 RCAEval / RE2-OB（MIT），已知模式范围内的辅助排查实验，不等同于生产根因确认或自动修复。<a href="https://huggingface.co/datasets/phamquiluan/RCAEval" target="_blank" rel="noopener noreferrer">查看来源</a></footer>
  </div>
</template>

<script>
import { computed, onMounted, onBeforeUnmount, ref } from 'vue'
import api from '../utils/api'

export default {
  name: 'RCAExperiment',
  setup() {
    const catalog = ref(null), report = ref(null), reportError = ref(''), error = ref('')
    const split = ref('holdout'), selected = ref(''), evidenceService = ref('checkoutservice')
    const observation = ref(null), result = ref(null), reveal = ref(false), busy = ref(false)
    const controller = new AbortController()
    const options = { timeout: 65000, signal: controller.signal }
    const strategies = ['legacy', 'feature_only', 'case_based']
    const visibleCases = computed(() => (catalog.value?.cases || []).filter(c => c.split === split.value))
    const service = computed(() => observation.value?.services.find(s => s.name === evidenceService.value))
    const metricRows = computed(() => ['cpu', 'mem', 'latency-90', 'workload', 'socket'].map(k => service.value?.metrics[k]).filter(Boolean))
    const diagnosis = computed(() => result.value?.run.diagnosis)
    const caseReportRows = computed(() => (report.value?.cases || []).filter(row => row.strategy === 'case_based'))
    const time = stamp => new Date(stamp * 1000).toLocaleString('zh-CN', { timeZone: 'UTC', hour12: false }) + ' UTC'
    const count = n => Number(n || 0).toLocaleString('zh-CN')
    const value = n => Number.isFinite(n) ? Number(n.toPrecision(5)).toLocaleString('zh-CN') : '缺失'
    const faultName = key => ({ cpu: 'CPU 压力', mem: '内存压力', delay: '网络延迟', disk: '磁盘 I/O 压力', loss: '网络丢包', socket: 'Socket 故障' }[key] || key)
    const modeName = key => ({ legacy: 'A · 原文本规则', feature_only: 'B · 观测特征规则', case_based: 'C · 历史案例增强' }[key] || key)
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
      try { const response = await api.get(`/v1/experiments/rca/observations/${id}`, options); if (selected.value === id) observation.value = response.data } catch (e) { if (!controller.signal.aborted) error.value = e.response?.data?.message || '当前观测读取失败，请重试' }
    }
    const changeSplit = async () => { selected.value = visibleCases.value[0]?.id || ''; await loadObservation() }
    const openCase = async id => { split.value = 'holdout'; selected.value = id; await loadObservation(); window.requestAnimationFrame(() => document.querySelector('.rca-page')?.scrollTo({ top: 0, behavior: 'smooth' })) }
    const run = async strategy => {
      if (busy.value || !selected.value) return
      busy.value = true; error.value = ''; result.value = null; reveal.value = false
      try {
        const response = await api.post('/v1/experiments/rca/diagnose', { case_id: selected.value, strategy }, options)
        if (!response.data.run) throw new Error('登录状态或诊断响应无效')
        result.value = response.data
        if (result.value.run.diagnosis.candidates.length) evidenceService.value = result.value.run.diagnosis.candidates[0].service
      } catch (e) { if (!controller.signal.aborted) error.value = e.response?.data?.message || e.message || '诊断失败' } finally { busy.value = false }
    }
    onMounted(async () => {
      api.get('/v1/experiments/rca/report', options).then(r => { if (!r.data.metrics || !r.data.cases) throw new Error('登录状态或报告响应无效'); report.value = r.data }).catch(e => { reportError.value = e.response?.data?.message || '离线报告暂不可用' })
      try { const response = await api.get('/v1/experiments/rca', options); if (!response.data.cases) throw new Error('请重新登录后进入实验页'); catalog.value = response.data; await changeSplit() } catch (e) { if (!controller.signal.aborted) error.value = e.response?.data?.message || e.message }
    })
    onBeforeUnmount(() => controller.abort())
    return { catalog, report, reportError, error, split, selected, evidenceService, observation, result, reveal, busy, visibleCases, service, metricRows, diagnosis, caseReportRows, strategies, time, count, value, faultName, modeName, reportOutcome, spark, loadObservation, changeSplit, openCase, run }
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
