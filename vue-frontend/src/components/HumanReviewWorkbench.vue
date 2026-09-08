<template>
  <div class="review-overlay" role="dialog" aria-modal="true" aria-label="人工验收工作台" @keydown.esc="close">
    <section class="review-shell">
      <header class="review-header">
        <div>
          <small>HUMAN GATE · 当前登录账号独立进度</small>
          <h2>人工验收工作台</h2>
          <p>逐例判断、自动保存断点；不会批量代签、改写冻结数据集或切换线上策略。</p>
        </div>
        <button class="close-button" type="button" aria-label="关闭人工验收工作台" @click="close">关闭 ×</button>
      </header>

      <nav class="review-tabs" aria-label="人工验收类型">
        <button :class="{ active: activeTab === 'catalog' }" type="button" @click="switchTab('catalog')">
          Full 320 标签复核 <strong>{{ catalogProgress.reviewed }}/{{ catalogProgress.total || 320 }}</strong>
        </button>
        <button :class="{ active: activeTab === 'judge' }" type="button" @click="switchTab('judge')">
          Judge 30 人工校准 <strong>{{ judgeProgress }}/30</strong>
        </button>
      </nav>

      <main class="review-scroll">
        <section v-if="activeTab === 'catalog'" aria-label="Full 320 标签复核">
          <div class="gate-notice">
            <strong>你正在核对“输入与期望结果是否适合作为评测标准”</strong>
            <span>全部通过后也只进入独立封存与重跑，不能直接成为正式基线。</span>
          </div>

          <section v-if="catalogWorkbench?.dataset_card" class="governance-card warning-card">
            <div class="governance-heading">
              <div>
                <small>DATASET CARD · {{ catalogWorkbench.dataset_card.source_category }}</small>
                <h3>{{ catalogWorkbench.dataset_card.title }}</h3>
              </div>
              <span>治理版本 {{ catalogWorkbench.governance_version }} · SHA {{ shortHash(catalogWorkbench.governance_sha256) }}</span>
            </div>
            <strong class="boundary-title">合成契约回归集 ≠ 真实生产准确率</strong>
            <p><b>评测对象：</b>{{ catalogWorkbench.dataset_card.target_system }}</p>
            <p><b>用途：</b>{{ catalogWorkbench.dataset_card.purpose }}</p>
            <p><b>允许得出的结论：</b>{{ catalogWorkbench.dataset_card.allowed_claim }}</p>
            <div class="claim-grid">
              <b>不能据此声称</b>
              <ul><li v-for="claim in catalogWorkbench.dataset_card.forbidden_claims" :key="claim">{{ claim }}</li></ul>
            </div>
          </section>

          <div class="progress-grid">
            <div><strong>{{ catalogProgress.reviewed }}/{{ catalogProgress.total || 320 }}</strong><span>已复核</span></div>
            <div><strong>{{ catalogProgress.approved || 0 }}</strong><span>标签通过</span></div>
            <div><strong>{{ catalogProgress.rejected || 0 }}</strong><span>退回修正</span></div>
            <div><strong>{{ catalogProgress.pending ?? 320 }}</strong><span>待复核</span></div>
          </div>

          <div v-if="catalogSealStatus" :class="['seal-card', { ready: catalogSealStatus.eligible, sealed: !!catalogSealStatus.current_seal }]">
            <div>
              <strong v-if="catalogSealStatus.current_seal">不可变封存候选已生成 · {{ catalogSealStatus.current_seal.seal_id }}</strong>
              <strong v-else-if="catalogSealStatus.eligible">320 条均已通过，可创建不可变封存候选</strong>
              <strong v-else>封存准入未满足 · 还需复核 {{ catalogSealStatus.progress.pending }} 条，退回 {{ catalogSealStatus.progress.rejected }} 条</strong>
              <span>{{ catalogSealStatus.next_gate }}</span>
              <small v-if="catalogSealStatus.current_seal">Seal SHA {{ catalogSealStatus.current_seal.seal_sha256 }} · 仍需从封存目录重跑五类评测</small>
              <small v-else>Review Set {{ catalogSealStatus.review_set_sha256 }}</small>
            </div>
            <label v-if="catalogSealStatus.eligible && !catalogSealStatus.current_seal" class="seal-ack">
              <input v-model="catalogSealAcknowledged" type="checkbox">
              我确认请求生成只读封存候选；这不会冻结正式基线或切换策略。
            </label>
            <button v-if="catalogSealStatus.eligible && !catalogSealStatus.current_seal" type="button" :disabled="sealingCatalog || !catalogSealAcknowledged" @click="sealCatalog">
              {{ sealingCatalog ? '封存中…' : '生成封存候选' }}
            </button>
          </div>

          <div class="filter-row">
            <label>切片
              <select v-model="catalogSlice" @change="resetCatalogPage">
                <option value="">全部切片</option>
                <option v-for="slice in catalogSlices" :key="slice" :value="slice">{{ sliceLabel(slice) }}</option>
              </select>
            </label>
            <label>状态
              <select v-model="catalogStatus" @change="resetCatalogPage">
                <option value="pending">待复核</option>
                <option value="rejected">退回修正</option>
                <option value="approved">标签通过</option>
                <option value="reviewed">全部已复核</option>
                <option value="all">全部状态</option>
              </select>
            </label>
            <span v-if="catalogWorkbench">筛选 {{ catalogWorkbench.filtered_total }} 条 · 第 {{ catalogWorkbench.page }}/{{ catalogPageCount }} 条</span>
          </div>

          <div v-if="loadingCatalog" class="loading-state">正在读取固定 Catalog 与当前账号进度…</div>
          <article v-else-if="catalogCase" class="case-card">
            <div class="case-title">
              <div>
                <small>{{ sliceLabel(catalogCase.slice) }} · {{ catalogCase.dataset_version }}</small>
                <h3>{{ catalogCase.id }}</h3>
              </div>
              <span v-if="catalogCase.review" class="reviewed-badge">已复核 · revision {{ catalogCase.review.revision }}</span>
            </div>
            <div class="prompt-box">
              <small>输入 / 场景</small>
              <p>{{ catalogCase.prompt }}</p>
            </div>

            <section v-if="catalogCase.review_guide" class="review-guide">
              <div class="guide-badges">
                <span>{{ catalogCase.review_guide.truth_title }}</span>
                <span>场景 {{ scopeLabel(catalogCase.review_guide.scenario_scope) }}</span>
                <span>阶段 {{ stageLabel(catalogCase.review_guide.evaluation_stage) }}</span>
              </div>
              <h4>这条数据为什么存在</h4>
              <p>{{ catalogCase.review_guide.purpose }}</p>
              <p class="truth-explanation"><b>真值如何产生：</b>{{ catalogCase.review_guide.truth_meaning }}</p>
              <div class="review-question">
                <small>你真正需要判断的问题</small>
                <strong>{{ catalogCase.review_guide.review_question }}</strong>
              </div>
              <div class="criteria-grid">
                <div>
                  <b>满足这些条件才通过</b>
                  <ul><li v-for="criterion in catalogCase.review_guide.pass_criteria" :key="criterion">{{ criterion }}</li></ul>
                </div>
                <div>
                  <b>出现任一情况应退回</b>
                  <ul><li v-for="criterion in catalogCase.review_guide.reject_criteria" :key="criterion">{{ criterion }}</li></ul>
                </div>
              </div>
              <div v-if="catalogCase.review_guide.evidence_excerpts?.length" class="evidence-excerpts">
                <b>用于独立核对的 Fixture 原文</b>
                <article v-for="evidence in catalogCase.review_guide.evidence_excerpts" :key="evidence.id">
                  <small>[{{ evidence.id }}] {{ evidence.document }} · {{ evidence.section }} · L{{ evidence.line_start }}-{{ evidence.line_end }}</small>
                  <p>{{ evidence.content }}</p>
                </article>
              </div>
              <div class="source-list">
                <b>独立规则 / 证据来源（均受 SHA 绑定）</b>
                <span v-for="source in catalogCase.review_guide.source_refs" :key="`${source.path}-${source.sha256}`">
                  {{ source.title }} · {{ source.path }} · SHA {{ shortHash(source.sha256) }}
                </span>
              </div>
              <p class="evidence-boundary"><b>边界：</b>{{ catalogCase.review_guide.evidence_boundary }}</p>
            </section>

            <details class="structured-case">
              <summary>展开结构化输入与期望结果</summary>
              <pre>{{ formatJSON(catalogCase.content) }}</pre>
            </details>
            <small class="hash-line">Case SHA {{ catalogCase.case_sha256 }}</small>

            <fieldset class="decision-fieldset">
              <legend>本例人工结论</legend>
              <label><input v-model="catalogDecision" type="radio" value="approved"> 标签、期望结果与安全边界均正确</label>
              <label><input v-model="catalogDecision" type="radio" value="rejected"> 存在问题，退回数据修正</label>
              <select v-if="catalogDecision === 'rejected'" v-model="catalogRejectReason">
                <option value="expected_result_incorrect">期望结果不正确</option>
                <option value="ambiguous_input">输入有歧义</option>
                <option value="missing_context">缺少上下文</option>
                <option value="schema_issue">Schema 问题</option>
                <option value="unsafe_or_sensitive">不安全或敏感</option>
                <option value="provenance_missing">真值来源缺失或不可核对</option>
                <option value="criterion_not_independent">判定标准依赖 expected 自证</option>
                <option value="scenario_not_representative">场景不具备目标范围内的代表性</option>
                <option value="not_discriminative">用例不能区分正确与错误实现</option>
              </select>
            </fieldset>
            <label class="ack-row">
              <input v-model="catalogAcknowledged" type="checkbox">
              我已先依据上方独立规则或证据推导结论，再核对 expected；这不是批量确认。
            </label>
            <div class="action-row">
              <button type="button" :disabled="catalogPage <= 1 || loadingCatalog" @click="moveCatalog(-1)">上一例</button>
              <button class="primary" type="button" :disabled="submittingCatalog || !catalogAcknowledged" @click="submitCatalog">
                {{ submittingCatalog ? '保存中…' : (catalogDecision === 'approved' ? '确认本例通过并继续' : '确认退回并继续') }}
              </button>
              <button type="button" :disabled="catalogPage >= catalogPageCount || loadingCatalog" @click="moveCatalog(1)">下一例</button>
            </div>
          </article>
          <div v-else class="empty-state">
            <strong>{{ catalogProgress.pending === 0 ? '当前账号已完成全部 320 条复核' : '当前筛选没有待处理用例' }}</strong>
            <span>可以切换状态或切片查看已复核记录；系统不会自动修改你的判断。</span>
          </div>
        </section>

        <section v-else aria-label="Judge 30 人工校准">
          <div class="gate-notice">
            <strong>你正在独立评价答案质量，不是在猜 Judge 会打多少分</strong>
            <span>首次提交前隐藏 Judge 分数与答案样本类型；五维均按 0.25 粒度评分，满 30 条后才计算 κ。</span>
          </div>

          <section v-if="judgeAudit?.calibration_card" class="governance-card">
            <div class="governance-heading">
              <div>
                <small>CALIBRATION CARD · {{ judgeAudit.calibration_card.scenario_scope }}</small>
                <h3>{{ judgeAudit.calibration_card.title }}</h3>
              </div>
              <span>治理版本 {{ judgeAudit.governance_version }} · SHA {{ shortHash(judgeAudit.governance_sha256) }}</span>
            </div>
            <p><b>它校准什么：</b>{{ judgeAudit.calibration_card.purpose }}</p>
            <p><b>评分原则：</b>{{ judgeAudit.calibration_card.review_principle }}</p>
            <div class="claim-grid compact">
              <b>解释限制</b>
              <ul><li v-for="limitation in judgeAudit.calibration_card.limitations" :key="limitation">{{ limitation }}</li></ul>
            </div>
          </section>
          <div class="progress-grid">
            <div><strong>{{ judgeProgress }}/30</strong><span>已评分</span></div>
            <div><strong>{{ judgeProgress === 30 ? judgeAudit.agreement.linear_weighted_kappa.toFixed(4) : '待满 30 条' }}</strong><span>线性加权 κ</span></div>
            <div><strong>{{ percent(judgeAudit?.agreement?.exact_grade_agreement) }}</strong><span>等级精确一致</span></div>
            <div><strong>{{ judgeAudit?.agreement?.calibration_gate_passed ? '通过' : '未通过' }}</strong><span>κ ≥ 0.70</span></div>
          </div>

          <div v-if="loadingJudge" class="loading-state">正在读取固定校准集与真实 Judge 报告…</div>
          <article v-else-if="judgeCase" class="case-card">
            <div class="case-title">
              <div>
                <small>{{ judgeSliceLabel(judgeCase.slice) }} · {{ judgeIndex + 1 }}/{{ judgeAudit.case_count }}</small>
                <h3>{{ judgeCase.id }}</h3>
              </div>
              <div class="case-badges">
                <span v-if="judgeCase.answer_variant" class="variant-badge">样本覆盖：{{ variantLabel(judgeCase.answer_variant) }}</span>
                <span v-if="judgeCase.human_scores" class="reviewed-badge">已评分 · revision {{ judgeCase.review_revision }}</span>
              </div>
            </div>
            <div class="judge-copy">
              <section><small>问题</small><p>{{ judgeCase.question }}</p></section>
              <section><small>待评答案</small><p>{{ judgeCase.answer }}</p></section>
              <section><small>允许证据（封闭的合成证据包）</small><p v-if="!judgeCase.evidence.length">无；本题不允许借助常识补出精确事实，正确行为应是不猜测。</p><p v-for="evidence in judgeCase.evidence" :key="evidence.id"><b>[{{ evidence.id }}] {{ evidence.source_id }} · {{ originLabel(evidence.origin) }}</b><br>{{ evidence.content }}</p></section>
              <section><small>完整性检查清单（不是额外证据）</small><p>{{ judgeCase.expected_facts.join('；') || '无具体事实，应正确拒答。' }}</p></section>
              <section><small>禁止声明</small><p>{{ judgeCase.forbidden_claims.join('；') || '无额外禁止声明。' }}</p></section>
            </div>
            <section v-if="judgeAudit.calibration_card?.score_anchors" class="score-rubric">
              <b>统一评分锚点</b>
              <div>
                <span v-for="anchor in judgeAudit.calibration_card.score_anchors" :key="anchor.value">
                  <strong>{{ Number(anchor.value).toFixed(anchor.value % 1 === 0 ? 0 : 2) }} · {{ anchor.label }}</strong>{{ anchor.meaning }}
                </span>
              </div>
            </section>
            <div class="score-grid">
              <label v-for="dimension in judgeDimensions" :key="dimension.value">
                <span>{{ dimension.label }}</span>
                <small>{{ judgeDimensionQuestion(dimension.value) }}</small>
                <select v-model="judgeDraft[dimension.value]">
                  <option value="" disabled>请选择</option>
                  <option v-for="option in scoreOptions" :key="option.value" :value="option.value">{{ option.label }}</option>
                </select>
              </label>
            </div>
            <div v-if="judgeCase.human_scores" class="judge-comparison">
              <strong>提交后对照（不会影响已保存的人工分）</strong>
              <span v-for="dimension in judgeDimensions" :key="dimension.value">
                {{ dimension.label }}：人工 {{ percent(judgeCase.human_scores[dimension.value]) }} / Judge {{ percent(judgeCase.judge.scores[dimension.value]) }}
              </span>
            </div>
            <div class="action-row">
              <button type="button" :disabled="judgeIndex === 0" @click="selectJudge(judgeIndex - 1)">上一条</button>
              <button class="primary" type="button" :disabled="submittingJudge || !judgeDraftComplete" @click="submitJudge">
                {{ submittingJudge ? '保存中…' : (judgeCase.human_scores ? '追加评分修订' : '提交人工评分并继续') }}
              </button>
              <button type="button" :disabled="judgeIndex >= judgeAudit.case_count - 1" @click="selectJudge(judgeIndex + 1)">下一条</button>
            </div>
            <div class="case-index" aria-label="Judge 校准用例索引">
              <button v-for="(item, index) in judgeAudit.cases" :key="item.id" :class="{ active: index === judgeIndex, reviewed: !!item.human_scores }" type="button" @click="selectJudge(index)">{{ index + 1 }}</button>
            </div>
          </article>
          <div v-else class="empty-state"><strong>Judge 校准报告暂不可用</strong><span>系统不会用模拟分数替代真实报告。</span></div>
        </section>
      </main>

      <footer class="review-footer">
        <span>刷新或关闭后可从当前账号进度继续；所有修改均追加 revision。</span>
        <button type="button" :disabled="activeTab === 'catalog' ? loadingCatalog : loadingJudge" @click="reloadActive">刷新当前进度</button>
      </footer>
    </section>
  </div>
</template>

<script>
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

const emptyJudgeDraft = () => ({ relevance: '', completeness: '', helpfulness: '', groundedness: '', safety: '' })

export default {
  name: 'HumanReviewWorkbench',
  emits: ['close'],
  setup (_, { emit }) {
    const activeTab = ref('catalog')
    const catalogWorkbench = ref(null)
    const catalogSlice = ref('')
    const catalogStatus = ref('pending')
    const catalogPage = ref(1)
    const catalogDecision = ref('approved')
    const catalogRejectReason = ref('expected_result_incorrect')
    const catalogAcknowledged = ref(false)
    const catalogIdempotencyKey = ref('')
    const loadingCatalog = ref(false)
    const submittingCatalog = ref(false)
    const catalogSealStatus = ref(null)
    const loadingCatalogSeal = ref(false)
    const sealingCatalog = ref(false)
    const catalogSealAcknowledged = ref(false)
    const judgeAudit = ref(null)
    const judgeIndex = ref(0)
    const judgeDraft = ref(emptyJudgeDraft())
    const loadingJudge = ref(false)
    const submittingJudge = ref(false)
    const catalogSlices = ['intent', 'rag', 'diagnosis', 'tool', 'memory', 'insufficient_evidence']
    const judgeDimensions = [
      { value: 'relevance', label: '相关性' }, { value: 'completeness', label: '完整性' }, { value: 'helpfulness', label: '有用性' },
      { value: 'groundedness', label: '有依据' }, { value: 'safety', label: '安全性' }
    ]
    const scoreOptions = [
      { value: 0, label: '0 · 完全不满足' }, { value: 0.25, label: '0.25 · 较差' }, { value: 0.5, label: '0.50 · 部分满足' },
      { value: 0.75, label: '0.75 · 基本满足' }, { value: 1, label: '1.00 · 完全满足' }
    ]
    const catalogProgress = computed(() => catalogWorkbench.value?.progress || { total: 320, reviewed: 0, approved: 0, rejected: 0, pending: 320 })
    const catalogCase = computed(() => catalogWorkbench.value?.cases?.[0] || null)
    const catalogPageCount = computed(() => Math.max(1, Math.ceil((catalogWorkbench.value?.filtered_total || 0) / (catalogWorkbench.value?.page_size || 1))))
    const judgeCase = computed(() => judgeAudit.value?.cases?.[judgeIndex.value] || null)
    const judgeProgress = computed(() => judgeAudit.value?.agreement?.reviewed_cases || 0)
    const judgeDraftComplete = computed(() => judgeDimensions.every(dimension => judgeDraft.value[dimension.value] !== ''))

    const close = () => emit('close')
    const formatJSON = value => JSON.stringify(value || {}, null, 2)
    const shortHash = value => value ? `${String(value).slice(0, 12)}…` : '不可用'
    const percent = value => Number.isFinite(value) ? `${(value * 100).toFixed(1)}%` : '待计算'
    const sliceLabel = slice => ({ intent: '意图识别', rag: 'RAG', diagnosis: '故障诊断', tool: '工具治理', memory: '三级记忆', insufficient_evidence: '证据不足' }[slice] || slice)
    const judgeSliceLabel = slice => ({ rag_single_fact: 'RAG 单事实', rag_cross_document: 'RAG 跨文档', insufficient_evidence: '证据不足', diagnosis: '故障诊断', tool_governance: '工具治理', memory: '三级记忆' }[slice] || slice)
    const scopeLabel = scope => ({ synthetic_user_request: '合成用户请求', closed_synthetic_knowledge_fixture: '封闭合成知识库', synthetic_incident_snapshot: '合成故障快照', synthetic_tool_lifecycle: '合成工具生命周期', synthetic_memory_facts: '合成记忆事实', synthetic_safety_boundary: '合成安全边界' }[scope] || scope)
    const stageLabel = stage => ({ intent_classification: '意图分类', retrieval_and_grounded_answer: '检索与有据回答', bounded_diagnostic_planning: '有界诊断规划', selection_schema_authorization_resilience_or_safety: '选择/Schema/授权/韧性/安全', profile_selection_and_context_budget: '画像选择与上下文预算', evidence_authority_gate: '证据权限门禁' }[stage] || stage)
    const variantLabel = variant => ({ strong: '高质量答案', incomplete: '不完整答案', factually_wrong: '事实错误答案', irrelevant: '偏题答案', unsafe: '不安全答案' }[variant] || variant)
    const originLabel = origin => ({ embedded_synthetic_evidence: '题内合成证据' }[origin] || origin)
    const judgeDimensionQuestion = id => judgeAudit.value?.calibration_card?.dimensions?.find(item => item.id === id)?.question || ''

    const loadCatalog = async () => {
      if (loadingCatalog.value) return
      try {
        loadingCatalog.value = true
        const response = await api.get('/evaluations/catalog/reviews', { params: { slice: catalogSlice.value || undefined, status: catalogStatus.value, page: catalogPage.value, page_size: 1 } })
        catalogWorkbench.value = response.data
        if (!response.data?.cases?.length && response.data?.filtered_total > 0 && catalogPage.value > 1) {
          catalogPage.value = Math.max(1, Math.ceil(response.data.filtered_total / response.data.page_size))
          loadingCatalog.value = false
          await loadCatalog()
        }
      } catch (error) {
        catalogWorkbench.value = null
        ElMessage.error(error.response?.data?.message || 'Full 320 复核队列暂不可用')
      } finally {
        loadingCatalog.value = false
      }
    }

    const loadCatalogSeal = async () => {
      if (loadingCatalogSeal.value) return
      try {
        loadingCatalogSeal.value = true
        const response = await api.get('/evaluations/catalog/reviews/seal/latest')
        catalogSealStatus.value = response.data
      } catch (error) {
        catalogSealStatus.value = null
        ElMessage.error(error.response?.data?.message || 'Full 320 封存状态暂不可用')
      } finally {
        loadingCatalogSeal.value = false
      }
    }

    const resetCatalogDecision = () => {
      catalogAcknowledged.value = false
      catalogIdempotencyKey.value = ''
      catalogDecision.value = catalogCase.value?.review?.decision || 'approved'
      catalogRejectReason.value = catalogCase.value?.review?.reason_codes?.[0] || 'expected_result_incorrect'
    }

    const resetCatalogPage = async () => {
      catalogPage.value = 1
      await loadCatalog()
      resetCatalogDecision()
    }

    const moveCatalog = async offset => {
      const next = catalogPage.value + offset
      if (next < 1 || next > catalogPageCount.value) return
      catalogPage.value = next
      await loadCatalog()
      resetCatalogDecision()
    }

    const submitCatalog = async () => {
      const item = catalogCase.value
      if (!item || submittingCatalog.value || !catalogAcknowledged.value) return
      if (!catalogIdempotencyKey.value) {
        const randomPart = window.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`
        catalogIdempotencyKey.value = `catalog-review-${randomPart}`
      }
      try {
        submittingCatalog.value = true
        await api.post('/evaluations/catalog/reviews', {
          mode: 'human_catalog_case_review', catalog_sha256: catalogWorkbench.value.catalog_sha256,
          governance_sha256: catalogWorkbench.value.governance_sha256, case_id: item.id,
          case_sha256: item.case_sha256, expected_revision: item.review?.revision || 0, decision: catalogDecision.value,
          reason_codes: catalogDecision.value === 'approved' ? ['label_verified'] : [catalogRejectReason.value],
          idempotency_key: catalogIdempotencyKey.value, acknowledgment: 'I_REVIEWED_CASE_AND_EXPECTED_RESULT'
        })
        ElMessage.success(`${item.id} 已保存；正在定位下一条待复核用例`)
        await loadCatalog()
        await loadCatalogSeal()
        resetCatalogDecision()
      } catch (error) {
        if (error.response?.status === 409) {
          catalogIdempotencyKey.value = ''
          await loadCatalog()
        }
        ElMessage.error(error.response?.data?.message || '本例复核保存失败')
      } finally {
        submittingCatalog.value = false
      }
    }

    const sealCatalog = async () => {
      if (!catalogSealStatus.value?.eligible || catalogSealStatus.value.current_seal || !catalogSealAcknowledged.value || sealingCatalog.value) return
      try {
        sealingCatalog.value = true
        const response = await api.post('/evaluations/catalog/reviews/seal', {
          mode: 'materialize_reviewed_catalog_candidate',
          catalog_sha256: catalogSealStatus.value.catalog_sha256,
          review_set_sha256: catalogSealStatus.value.review_set_sha256,
          acknowledgment: 'I_CONFIRM_320_APPROVED_AND_REQUEST_SEALED_CANDIDATE'
        })
        catalogSealAcknowledged.value = false
        await loadCatalogSeal()
        ElMessage.success(`${response.data.created ? '已创建' : '已复用'}不可变封存候选；正式基线仍需重跑与审批`)
      } catch (error) {
        catalogSealAcknowledged.value = false
        await loadCatalogSeal()
        ElMessage.error(error.response?.data?.message || 'Full 320 封存候选生成失败')
      } finally {
        sealingCatalog.value = false
      }
    }

    const selectJudge = index => {
      const cases = judgeAudit.value?.cases || []
      if (!Number.isInteger(index) || index < 0 || index >= cases.length) return
      judgeIndex.value = index
      const scores = cases[index].human_scores
      judgeDraft.value = scores ? { ...scores } : emptyJudgeDraft()
    }

    const loadJudge = async preferredIndex => {
      if (loadingJudge.value) return
      try {
        loadingJudge.value = true
        const response = await api.get('/evaluations/judge-calibration/latest')
        judgeAudit.value = response.data
        let index = Number.isInteger(preferredIndex) ? preferredIndex : response.data.cases.findIndex(item => !item.human_scores)
        if (index < 0 || index >= response.data.cases.length) index = 0
        selectJudge(index)
      } catch (error) {
        judgeAudit.value = null
        ElMessage.error(error.response?.data?.message || 'Judge 真实校准报告暂不可用')
      } finally {
        loadingJudge.value = false
      }
    }

    const submitJudge = async () => {
      if (!judgeCase.value || submittingJudge.value || !judgeDraftComplete.value) return
      try {
        submittingJudge.value = true
        const currentID = judgeCase.value.id
        const currentIndex = judgeIndex.value
        await api.post('/evaluations/judge-calibration/reviews', { case_id: currentID, scores: judgeDraft.value })
        await loadJudge(currentIndex)
        const nextPending = judgeAudit.value.cases.findIndex((item, index) => index > currentIndex && !item.human_scores)
        selectJudge(nextPending >= 0 ? nextPending : currentIndex)
        ElMessage.success('人工评分已追加保存')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '人工评分保存失败')
      } finally {
        submittingJudge.value = false
      }
    }

    const switchTab = async tab => {
      activeTab.value = tab
      if (tab === 'catalog' && !catalogWorkbench.value) await Promise.all([loadCatalog(), loadCatalogSeal()])
      if (tab === 'judge' && !judgeAudit.value) await loadJudge()
    }
    const reloadActive = () => activeTab.value === 'catalog' ? Promise.all([loadCatalog(), loadCatalogSeal()]) : loadJudge(judgeIndex.value)
    const preventBodyScroll = () => { document.body.style.overflow = 'hidden' }
    const restoreBodyScroll = () => { document.body.style.overflow = '' }

    onMounted(async () => {
      preventBodyScroll()
      await Promise.all([loadCatalog(), loadCatalogSeal(), loadJudge()])
      resetCatalogDecision()
    })
    onUnmounted(restoreBodyScroll)

    return {
      activeTab, catalogWorkbench, catalogSlice, catalogStatus, catalogPage, catalogDecision, catalogRejectReason,
      catalogAcknowledged, loadingCatalog, submittingCatalog, catalogSlices, catalogProgress, catalogCase, catalogPageCount,
      catalogSealStatus, sealingCatalog, catalogSealAcknowledged,
      judgeAudit, judgeIndex, judgeDraft, loadingJudge, submittingJudge, judgeDimensions, scoreOptions, judgeCase, judgeProgress,
      judgeDraftComplete, close, formatJSON, shortHash, percent, sliceLabel, judgeSliceLabel, scopeLabel, stageLabel,
      variantLabel, originLabel, judgeDimensionQuestion, switchTab, resetCatalogPage, moveCatalog,
      submitCatalog, sealCatalog, selectJudge, submitJudge, reloadActive
    }
  }
}
</script>

<style scoped>
.review-overlay { position: fixed; inset: 0; z-index: 3000; padding: 24px; background: rgba(22, 19, 55, .68); backdrop-filter: blur(6px); box-sizing: border-box; }
.review-shell { width: min(1180px, 100%); height: 100%; margin: 0 auto; display: grid; grid-template-rows: auto auto minmax(0, 1fr) auto; overflow: hidden; border-radius: 22px; background: #f7f8ff; box-shadow: 0 24px 80px rgba(14, 10, 52, .35); color: #27324b; }
.review-header { display: flex; justify-content: space-between; gap: 24px; padding: 22px 28px 16px; background: linear-gradient(135deg, #5a67d8, #7652c8); color: white; }
.review-header small { font-weight: 700; letter-spacing: .08em; opacity: .85; }
.review-header h2 { margin: 5px 0 4px; font-size: 25px; }
.review-header p { margin: 0; opacity: .9; }
button, select { font: inherit; }
button { cursor: pointer; }
button:disabled { cursor: not-allowed; opacity: .45; }
.close-button { align-self: flex-start; padding: 9px 14px; border: 1px solid rgba(255,255,255,.45); border-radius: 10px; background: rgba(255,255,255,.12); color: white; }
.review-tabs { display: grid; grid-template-columns: 1fr 1fr; padding: 10px 28px 0; background: white; border-bottom: 1px solid #e3e6f5; }
.review-tabs button { padding: 14px; border: 0; border-bottom: 3px solid transparent; background: transparent; color: #63708a; }
.review-tabs button.active { border-color: #6555d8; color: #493aac; }
.review-tabs strong { margin-left: 8px; }
.review-scroll { min-height: 0; overflow-y: auto; padding: 22px 28px 36px; overscroll-behavior: contain; }
.gate-notice { display: flex; flex-direction: column; gap: 4px; padding: 14px 16px; border-left: 4px solid #7a62dc; border-radius: 9px; background: #edeafd; }
.gate-notice span, .filter-row span, .hash-line, .review-footer, .case-title small { color: #6c7690; }
.governance-card { margin-top: 14px; padding: 16px 18px; border: 1px solid #cec7f4; border-radius: 12px; background: #f5f3ff; }
.governance-card.warning-card { border-color: #edcf7b; background: #fff9e8; }
.governance-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 18px; }
.governance-heading small { color: #7567bd; font-weight: 700; letter-spacing: .04em; }
.governance-heading h3 { margin: 3px 0 0; font-size: 18px; }
.governance-heading > span { color: #7b8499; font-family: monospace; font-size: 11px; overflow-wrap: anywhere; }
.governance-card p { margin: 8px 0 0; line-height: 1.6; }
.boundary-title { display: block; margin-top: 13px; color: #a45c05; font-size: 17px; }
.claim-grid { display: grid; grid-template-columns: 150px 1fr; gap: 14px; margin-top: 12px; padding: 12px 14px; border-radius: 9px; background: rgba(255,255,255,.72); }
.claim-grid.compact { grid-template-columns: 100px 1fr; }
.claim-grid ul, .criteria-grid ul { margin: 0; padding-left: 19px; }
.claim-grid li, .criteria-grid li { margin: 3px 0; line-height: 1.5; }
.progress-grid { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin: 16px 0; }
.progress-grid div { display: flex; flex-direction: column; padding: 14px 16px; border: 1px solid #e0e4f4; border-radius: 12px; background: white; }
.progress-grid strong { font-size: 23px; color: #5145b5; }
.progress-grid span { margin-top: 3px; color: #778199; font-size: 13px; }
.filter-row { display: flex; align-items: end; gap: 14px; flex-wrap: wrap; margin-bottom: 16px; }
.seal-card { display: grid; grid-template-columns: minmax(0, 1fr) auto auto; align-items: center; gap: 14px; margin-bottom: 16px; padding: 13px 15px; border: 1px solid #e4c878; border-radius: 11px; background: #fff8df; }
.seal-card.ready { border-color: #64b990; background: #eaf9f2; }
.seal-card.sealed { grid-template-columns: 1fr; border-color: #7363d6; background: #f0edff; }
.seal-card > div { display: grid; gap: 4px; min-width: 0; }
.seal-card span, .seal-card small { color: #6b7182; overflow-wrap: anywhere; font-size: 12px; line-height: 1.45; }
.seal-ack { display: flex; align-items: flex-start; gap: 7px; max-width: 310px; font-size: 12px; line-height: 1.45; }
.seal-ack input { margin-top: 3px; }
.seal-card button { min-height: 38px; padding: 7px 12px; border: 0; border-radius: 8px; background: #268862; color: white; font-weight: 700; }
.filter-row label, .score-grid label { display: flex; flex-direction: column; gap: 5px; color: #59647c; font-size: 13px; }
select { min-height: 38px; padding: 6px 10px; border: 1px solid #cfd5e8; border-radius: 8px; background: white; color: #27324b; }
.case-card { padding: 20px; border: 1px solid #dce1f2; border-radius: 16px; background: white; box-shadow: 0 8px 26px rgba(67, 59, 126, .07); }
.case-title { display: flex; justify-content: space-between; gap: 16px; align-items: center; }
.case-title h3 { margin: 3px 0 0; font-size: 21px; }
.reviewed-badge { padding: 5px 10px; border-radius: 999px; background: #def7e9; color: #18734a; font-size: 12px; font-weight: 700; }
.prompt-box, .judge-copy section { margin-top: 14px; padding: 13px 15px; border-radius: 10px; background: #f4f6fd; }
.prompt-box small, .judge-copy small { color: #6555b6; font-weight: 700; }
.prompt-box p, .judge-copy p { margin: 6px 0 0; white-space: pre-wrap; line-height: 1.65; }
.review-guide { margin-top: 14px; padding: 16px; border: 1px solid #cfdcf4; border-radius: 12px; background: #f8fbff; }
.review-guide h4 { margin: 14px 0 6px; color: #3b4771; }
.review-guide > p { margin: 5px 0; line-height: 1.6; }
.guide-badges, .case-badges { display: flex; align-items: center; justify-content: flex-end; gap: 7px; flex-wrap: wrap; }
.guide-badges { justify-content: flex-start; }
.guide-badges span, .variant-badge { padding: 5px 9px; border-radius: 999px; background: #e7e2ff; color: #5542ad; font-size: 12px; font-weight: 700; }
.truth-explanation { color: #505c78; }
.review-question { display: grid; gap: 5px; margin-top: 13px; padding: 12px 14px; border-left: 4px solid #6555d8; border-radius: 8px; background: #eeebff; }
.review-question small { color: #6857bb; font-weight: 700; }
.criteria-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-top: 13px; }
.criteria-grid > div { padding: 12px 14px; border-radius: 9px; background: #eaf8f1; }
.criteria-grid > div + div { background: #fff1ef; }
.criteria-grid ul { margin-top: 7px; }
.source-list { display: grid; gap: 5px; margin-top: 13px; padding: 12px 14px; border-radius: 9px; background: #f0f2f8; }
.source-list span { color: #647089; font-family: monospace; font-size: 11px; overflow-wrap: anywhere; }
.evidence-excerpts { margin-top: 13px; }
.evidence-excerpts > article { margin-top: 8px; padding: 11px 13px; border: 1px solid #cfe5dc; border-radius: 9px; background: #edf9f4; }
.evidence-excerpts small { color: #287356; font-weight: 700; }
.evidence-excerpts p { margin: 6px 0 0; white-space: pre-wrap; line-height: 1.6; }
.evidence-boundary { margin-top: 13px !important; color: #8b5a12; }
.structured-case { margin-top: 14px; border: 1px solid #e2e5f1; border-radius: 10px; overflow: hidden; }
.structured-case summary { padding: 11px 14px; background: #fafaff; color: #5547aa; font-weight: 700; cursor: pointer; }
.structured-case pre { max-height: 380px; margin: 0; padding: 16px; overflow: auto; white-space: pre-wrap; overflow-wrap: anywhere; background: #25283b; color: #f3f5ff; line-height: 1.55; }
.hash-line { display: block; margin-top: 9px; overflow-wrap: anywhere; font-family: monospace; }
.decision-fieldset { display: flex; flex-direction: column; gap: 10px; margin: 18px 0 12px; padding: 14px 16px; border: 1px solid #d8dced; border-radius: 10px; }
.decision-fieldset legend { padding: 0 6px; font-weight: 700; }
.ack-row { display: flex; align-items: flex-start; gap: 8px; padding: 12px 14px; border-radius: 9px; background: #fff5d9; }
.ack-row input { margin-top: 3px; }
.action-row { display: grid; grid-template-columns: 130px 1fr 130px; gap: 12px; margin-top: 18px; }
.action-row button, .review-footer button { min-height: 42px; border: 1px solid #cbd1e4; border-radius: 9px; background: white; color: #4e5871; font-weight: 700; }
.action-row .primary { border-color: #6555d8; background: #6555d8; color: white; }
.score-grid { display: grid; grid-template-columns: repeat(5, 1fr); gap: 10px; margin-top: 16px; }
.score-grid label small { min-height: 58px; color: #7a8396; line-height: 1.45; }
.score-rubric { display: grid; gap: 9px; margin-top: 14px; padding: 13px 15px; border: 1px solid #dedaf2; border-radius: 10px; background: #faf9ff; }
.score-rubric > div { display: grid; grid-template-columns: repeat(5, 1fr); gap: 8px; }
.score-rubric span { display: flex; flex-direction: column; gap: 4px; padding: 9px; border-radius: 8px; background: white; color: #69738a; font-size: 11px; line-height: 1.45; }
.score-rubric strong { color: #5145b5; font-size: 12px; }
.judge-comparison { display: flex; flex-wrap: wrap; gap: 8px 18px; margin-top: 14px; padding: 13px; border-radius: 10px; background: #eaf8f1; color: #28634c; }
.judge-comparison strong { width: 100%; }
.case-index { display: grid; grid-template-columns: repeat(15, 1fr); gap: 5px; margin-top: 18px; }
.case-index button { aspect-ratio: 1; border: 1px solid #d7dbee; border-radius: 7px; background: #f5f6fb; color: #667087; }
.case-index button.reviewed { border-color: #72c49b; background: #e2f7ec; color: #196d48; }
.case-index button.active { outline: 2px solid #6555d8; outline-offset: 1px; }
.loading-state, .empty-state { display: flex; flex-direction: column; align-items: center; gap: 7px; padding: 60px 20px; border: 1px dashed #cbd1e8; border-radius: 14px; background: white; color: #6a748e; }
.review-footer { display: flex; align-items: center; justify-content: space-between; gap: 20px; padding: 12px 28px; border-top: 1px solid #e1e4f1; background: white; font-size: 13px; }
.review-footer button { min-height: 34px; padding: 4px 14px; }

/* Independent human-gate route: industrial dark presentation. */
.review-overlay { position: absolute; padding: 12px; background: rgba(3, 8, 12, .88); backdrop-filter: blur(4px); }
.review-shell {
  width: min(1320px, 100%);
  border: 1px solid var(--g-border-strong);
  border-radius: 2px;
  color: var(--g-text-primary);
  background: var(--g-bg);
  box-shadow: 0 24px 80px rgba(0, 0, 0, .5);
}
.review-header {
  padding: 18px 24px 14px;
  border-bottom: 1px solid var(--g-border-strong);
  background: linear-gradient(100deg, rgba(0, 229, 255, .08), transparent 55%), var(--g-panel);
  box-shadow: inset 3px 0 0 var(--g-primary);
}
.review-header small { color: var(--g-primary); font-family: var(--g-font-mono); }
.review-header p { color: var(--g-text-secondary); }
.close-button,
.action-row button,
.review-footer button,
.seal-card button {
  border: 1px solid var(--g-border-strong);
  border-radius: 2px;
  color: var(--g-text-secondary);
  background: #0e1922;
}
.review-tabs { border-color: var(--g-border); background: #0e171f; }
.review-tabs button { color: var(--g-text-muted); }
.review-tabs button.active { color: var(--g-primary); border-color: var(--g-primary); background: rgba(0, 229, 255, .04); }
.review-scroll { background: #0b131a; }
.gate-notice,
.governance-card,
.governance-card.warning-card,
.claim-grid,
.progress-grid div,
.seal-card,
.seal-card.ready,
.seal-card.sealed,
.case-card,
.prompt-box,
.judge-copy section,
.review-guide,
.review-question,
.criteria-grid > div,
.criteria-grid > div + div,
.source-list,
.evidence-excerpts > article,
.structured-case,
.structured-case summary,
.decision-fieldset,
.ack-row,
.score-rubric,
.score-rubric span,
.judge-comparison,
.loading-state,
.empty-state,
.review-footer {
  border-color: var(--g-border) !important;
  border-radius: 2px !important;
  color: var(--g-text-secondary) !important;
  background: var(--g-panel-raised) !important;
  box-shadow: none !important;
}
.gate-notice, .review-question { border-left-color: var(--g-primary) !important; }
.review-shell h2, .review-shell h3, .review-shell h4, .review-shell strong, .review-shell b { color: var(--g-text-primary); }
.gate-notice span, .filter-row span, .hash-line, .review-footer, .case-title small,
.governance-heading > span, .progress-grid span, .seal-card span, .seal-card small,
.filter-row label, .score-grid label, .score-grid label small, .truth-explanation { color: var(--g-text-secondary); }
.governance-heading small, .prompt-box small, .judge-copy small, .review-question small,
.score-rubric strong, .structured-case summary { color: var(--g-primary) !important; }
.progress-grid strong { color: var(--g-primary); font-family: var(--g-font-mono); }
.boundary-title, .evidence-boundary { color: var(--g-warning) !important; }
.guide-badges span, .variant-badge, .reviewed-badge {
  border: 1px solid var(--g-border-strong);
  border-radius: 2px;
  color: var(--g-primary);
  background: rgba(0, 229, 255, .06);
}
.evidence-excerpts small { color: var(--g-success); }
select {
  border: 1px solid var(--g-border-strong);
  border-radius: 2px;
  color: var(--g-text-primary);
  background: #081118;
}
.structured-case pre { color: #cfedf3; background: #071017; }
.action-row .primary { color: #041015; border-color: var(--g-primary); background: var(--g-primary); }
.case-index button { border-color: var(--g-border); border-radius: 2px; color: var(--g-text-muted); background: #101a23; }
.case-index button.reviewed { border-color: rgba(32, 227, 162, .4); color: var(--g-success); background: rgba(32, 227, 162, .08); }
.case-index button.active { outline-color: var(--g-primary); }
.judge-comparison { color: var(--g-success) !important; }
@media (max-width: 900px) {
  .review-overlay { padding: 0; }
  .review-shell { border-radius: 0; }
  .review-header { padding: 16px; }
  .review-header p { display: none; }
  .review-tabs, .review-scroll { padding-left: 14px; padding-right: 14px; }
  .progress-grid { grid-template-columns: repeat(2, 1fr); }
  .score-grid { grid-template-columns: repeat(2, 1fr); }
  .score-rubric > div { grid-template-columns: 1fr; }
  .governance-heading, .case-title { align-items: flex-start; flex-direction: column; }
  .claim-grid, .claim-grid.compact, .criteria-grid { grid-template-columns: 1fr; }
  .case-badges { justify-content: flex-start; }
  .seal-card { grid-template-columns: 1fr; }
  .action-row { grid-template-columns: 1fr; }
  .case-index { grid-template-columns: repeat(10, 1fr); }
  .review-footer { padding: 10px 14px; }
}
</style>
