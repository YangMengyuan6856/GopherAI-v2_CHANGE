<template>
  <div class="ai-chat-container">
    <!-- 左侧会话列表 -->
    <div class="session-list">
      <div class="session-list-header">
        <span>会话列表</span>
        <button class="new-chat-btn" @click="createNewSession">＋ 新聊天</button>
      </div>
      <ul class="session-list-ul">
        <li
          v-for="session in sessions"
          :key="session.id"
          :class="['session-item', { active: currentSessionId === session.id }]"
          @click="switchSession(session.id)"
        >
          {{ session.name || `会话 ${session.id}` }}
        </li>
      </ul>
    </div>

    <!-- 右侧聊天区域 -->
    <div class="chat-section">
      <div class="top-bar">
        <button class="back-btn" @click="$router.push('/menu')">← 返回</button>
        <button class="sync-btn" @click="syncHistory" :disabled="!currentSessionId || tempSession">同步历史数据</button>
        <span class="route-mode" title="新识别器只记录建议和指标；实际路由仍由当前显式开关决定">🧭 意图 Shadow（不切流）</span>
        <label for="streamingMode" style="margin-left: 20px;">
          <input type="checkbox" id="streamingMode" v-model="isStreaming" />
          流式响应
        </label>
        <label for="knowledgeMode" class="knowledge-mode" title="显式要求统一聊天入口使用 rag_fast；关闭时普通聊天保持原路径">
          <input type="checkbox" id="knowledgeMode" v-model="knowledgeRequired" />
          知识库回答
        </label>
        <label for="diagnosticMode" class="diagnostic-mode" title="显式进入可暂停、可恢复、有预算和公开步骤的故障诊断 Agent">
          <input type="checkbox" id="diagnosticMode" v-model="diagnosticMode" @change="onDiagnosticModeChanged" />
          故障诊断 Harness
        </label>
        <button class="memory-toggle-btn" :disabled="loadingMemoryPreview" @click="toggleMemoryPreview">🧠 三级记忆</button>
        <button class="tool-runtime-toggle" :disabled="loadingToolCatalog" @click="toggleToolRuntime">🛡 受治理工具</button>
        <button
          class="upload-btn"
          title="支持 Markdown/TXT、JSON/YAML key path 和 Go 顶层符号索引"
          @click="triggerFileUpload"
          :disabled="uploading"
        >📎 上传项目文档</button>
        <button class="search-toggle-btn" @click="toggleKnowledgeSearch">🔎 证据检索</button>
        <input
          ref="fileInput"
          type="file"
          accept=".md,.txt,.json,.yaml,.yml,.go,text/markdown,text/plain,application/json,application/yaml"
          style="display: none"
          @change="handleFileUpload"
        />
      </div>

      <section v-if="toolRuntimeOpen" class="tool-runtime-panel">
        <div class="tool-runtime-header">
          <div>
            <strong>受治理 Tool Runtime</strong>
            <span>Registry → Schema → 意图/权限/副作用 → 预算/超时 → 审计/指标</span>
          </div>
          <span class="tool-runtime-schema">{{ toolCatalog?.schema_version || 'tool-message-v1' }}</span>
        </div>
        <div v-if="loadingToolCatalog" class="tool-runtime-empty">正在读取服务端工具注册表...</div>
        <div v-else-if="toolCatalog?.tools?.length" class="tool-runtime-grid">
          <article v-for="tool in toolCatalog.tools" :key="`${tool.name}:${tool.version}`" class="tool-runtime-card">
            <div class="tool-runtime-title">
              <strong>{{ tool.name }}</strong>
              <span>v{{ tool.version }}</span>
            </div>
            <p>{{ tool.description }}</p>
            <div class="tool-runtime-tags">
              <span>{{ tool.side_effect }}</span>
              <span>{{ tool.timeout_ms }} ms</span>
              <span>{{ tool.required_permission }}</span>
              <span>意图：{{ tool.allowed_intents.join(' / ') }}</span>
            </div>
            <button
              v-if="tool.name === 'deployment_manifest_lookup'"
              :disabled="invokingTool"
              @click="invokeGovernedTool(tool.name, {})"
            >{{ invokingTool ? '执行治理链路中...' : '查询当前部署清单' }}</button>
            <div v-if="tool.name === 'service_health_snapshot'" class="tool-health-actions">
              <button :disabled="invokingTool" @click="invokeGovernedTool(tool.name, { service: 'backend', probe: 'ready' })">Backend Ready</button>
              <button :disabled="invokingTool" @click="invokeGovernedTool(tool.name, { service: 'index_worker', probe: 'ready' })">Worker Ready</button>
            </div>
          </article>
        </div>
        <div v-else class="tool-runtime-empty">当前没有通过治理注册的工具。</div>
        <article v-if="toolResult" :class="['tool-result', toolResult.status === 'success' ? 'success' : 'failed']">
          <div class="tool-result-heading">
            <strong>ToolMessage · {{ toolResult.status }}</strong>
            <span>{{ toolResult.tool_name }}@{{ toolResult.tool_version || 'unknown' }} · {{ toolResult.latency_ms }} ms</span>
          </div>
          <div>Call {{ toolResult.call_id }} · Args SHA-256 {{ (toolResult.args_hash || '').slice(0, 16) }}…</div>
          <div v-if="toolResult.error_code">稳定错误码：{{ toolResult.error_code }} · 可重试：{{ toolResult.retryable ? '是' : '否' }}</div>
          <pre v-if="toolResult.data">{{ formatToolData(toolResult.data) }}</pre>
          <div v-if="toolResult.evidence_refs?.length">证据：{{ toolResult.evidence_refs.join('；') }}</div>
          <small>缓存 {{ toolResult.cached ? '命中' : '未命中' }} · 截断 {{ toolResult.truncated ? '是' : '否' }} · 原始参数与用户标识不写入审计</small>
        </article>
      </section>

      <div v-if="knowledgeDocuments.length" class="knowledge-status">
        <span>📚 已接收 {{ knowledgeDocuments.length }} 份文档</span>
        <span class="knowledge-latest">
          最近：{{ knowledgeDocuments[0].display_name }} · {{ documentStatusLabel(knowledgeDocuments[0].status) }}
        </span>
        <div class="knowledge-version-controls">
          <select v-model="versionTargetDocumentId" title="选择要保留历史并更新版本的文档">
            <option value="" disabled>选择活动文档</option>
            <option v-for="document in indexedKnowledgeDocuments" :key="document.id" :value="document.id">
              {{ document.display_name }} · 当前 v{{ document.current_version }}
            </option>
          </select>
          <button :disabled="uploadingVersion || !versionTargetDocumentId" @click="triggerVersionUpload">
            {{ uploadingVersion ? '新版本上传中...' : '♻ 上传新版本' }}
          </button>
          <button :disabled="rebuildingDocument || !versionTargetDocumentId" @click="rebuildSelectedDocument">
            {{ rebuildingDocument ? '重建中...' : '↻ 安全重建' }}
          </button>
          <button class="delete-document-btn" :disabled="deletingDocument || !versionTargetDocumentId" @click="deleteSelectedDocument">
            {{ deletingDocument ? '删除中...' : '删除文档' }}
          </button>
          <span v-if="pendingVersionJob" class="version-pending">
            <template v-if="pendingVersionJob.job_type === 'document_delete'">
              已退出查询，{{ jobStatusLabel(pendingVersionJob.status) }}
            </template>
            <template v-else>
              v{{ pendingVersionJob.version }} {{ jobStatusLabel(pendingVersionJob.status) }}；旧版本继续生效
            </template>
          </span>
          <input
            ref="versionFileInput"
            type="file"
            accept=".md,.txt,.json,.yaml,.yml,.go,text/markdown,text/plain,application/json,application/yaml"
            style="display: none"
            @change="handleVersionUpload"
          />
        </div>
        <details class="knowledge-document-details">
          <summary>查看全部文档状态（{{ knowledgeDocuments.length }}）</summary>
          <div class="knowledge-document-grid">
            <article v-for="document in knowledgeDocuments" :key="document.id" class="knowledge-document-card">
              <div>
                <strong>{{ document.display_name }}</strong>
                <span :class="['document-status-badge', `status-${document.status}`]">
                  {{ documentStatusLabel(document.status) }}
                </span>
              </div>
              <div class="document-version-line">
                活动版本 v{{ document.current_version }} · {{ document.content_type || '未知格式' }}
              </div>
              <div v-if="document.last_error_code" class="document-error-code">
                稳定失败码：{{ document.last_error_code }}；失败候选不会替换活动版本
              </div>
            </article>
          </div>
        </details>
      </div>

      <div v-if="knowledgeSearchOpen" class="knowledge-search-panel">
        <div class="knowledge-search-form">
          <input
            v-model="knowledgeQuery"
            type="text"
            placeholder="输入错误码、配置名或项目问题，预览系统实际召回的证据"
            @keydown.enter.prevent="searchKnowledge"
          />
          <button :disabled="!knowledgeQuery.trim() || searchingKnowledge" @click="searchKnowledge">
            {{ searchingKnowledge ? '检索中...' : '执行混合检索' }}
          </button>
          <button
            class="answer-evidence-btn"
            :disabled="!knowledgeQuery.trim() || answeringKnowledge"
            @click="answerKnowledge(false)"
          >
            {{ answeringKnowledge && answeringKnowledgeMode === 'fast' ? '快速回答中...' : '基于证据回答' }}
          </button>
          <button
            class="deep-answer-btn"
            :disabled="!knowledgeQuery.trim() || answeringKnowledge"
            @click="answerKnowledge(true)"
          >
            {{ answeringKnowledge && answeringKnowledgeMode === 'deep' ? '深度检索与回答中...' : '深度分析回答' }}
          </button>
        </div>
        <section v-if="knowledgeAnswer" :class="['knowledge-answer', { insufficient: !knowledgeAnswer.result.resolved }]">
          <div class="knowledge-answer-header">
            <strong>{{ knowledgeAnswer.result.resolved ? '✅ 证据门通过' : '⚠️ 证据不足' }}</strong>
            <span>{{ knowledgeAnswer.agent }} · {{ knowledgeAnswer.strategy }} · {{ knowledgeAnswer.evidence_gate.reason_code }}</span>
          </div>
          <div class="knowledge-answer-text">{{ knowledgeAnswer.result.answer }}</div>
          <div v-if="knowledgeAnswer.diagnostics && knowledgeAnswer.diagnostics.deep" class="deep-diagnostics">
            <strong>深度策略：</strong>{{ deepOutcomeLabel(knowledgeAnswer.diagnostics.deep) }} ·
            追加检索 {{ knowledgeAnswer.diagnostics.deep.additional_searches }} 次 ·
            候选 {{ knowledgeAnswer.diagnostics.deep.candidates_before }} → {{ knowledgeAnswer.diagnostics.deep.candidates_after }} 条 ·
            Rewrite {{ enhancementOutcomeLabel(knowledgeAnswer.diagnostics.deep.rewrite.outcome_reason) }} ·
            Rerank {{ enhancementOutcomeLabel(knowledgeAnswer.diagnostics.deep.rerank.outcome_reason) }}
            <div class="deep-budget-observation">
              实际增强模型用量：输入 {{ knowledgeAnswer.diagnostics.deep.usage.input_tokens || 0 }} Token，
              输出 {{ knowledgeAnswer.diagnostics.deep.usage.output_tokens || 0 }} Token ·
              Rewrite {{ knowledgeAnswer.diagnostics.deep.rewrite.latency_ms || 0 }} ms ·
              Rerank {{ knowledgeAnswer.diagnostics.deep.rerank.latency_ms || 0 }} ms
            </div>
            <div v-if="knowledgeAnswer.diagnostics.deep.fallback_reasons && knowledgeAnswer.diagnostics.deep.fallback_reasons.length" class="deep-fallback-reasons">
              安全回退原因：{{ knowledgeAnswer.diagnostics.deep.fallback_reasons.map(enhancementOutcomeLabel).join('；') }}
            </div>
            <details v-if="knowledgeAnswer.diagnostics.deep.rewrite.queries && knowledgeAnswer.diagnostics.deep.rewrite.queries.length > 1">
              <summary>查看原查询与改写查询</summary>
              <ol>
                <li v-for="query in knowledgeAnswer.diagnostics.deep.rewrite.queries" :key="query">{{ query }}</li>
              </ol>
            </details>
          </div>
          <div v-if="knowledgeAnswer.result.follow_up_questions && knowledgeAnswer.result.follow_up_questions.length" class="knowledge-follow-up">
            <strong>需要补充：</strong>{{ knowledgeAnswer.result.follow_up_questions.join('；') }}
          </div>
          <div v-if="knowledgeAnswer.result.citations && knowledgeAnswer.result.citations.length" class="knowledge-citations">
            <details v-for="(citation, index) in knowledgeAnswer.result.citations" :key="citation.citation_id">
              <summary>
                [{{ index + 1 }}] {{ citation.document }} · v{{ citation.version }} ·
                {{ citation.section || '未命名章节' }} · L{{ citation.line_start }}-{{ citation.line_end }}
              </summary>
              <pre>{{ evidenceForCitation(citation).content || '证据内容不可用' }}</pre>
            </details>
          </div>
        </section>
        <div v-if="knowledgeSearchDiagnostics" class="knowledge-search-summary">
          {{ retrievalModeLabel(knowledgeSearchDiagnostics.mode) }} · Dense {{ knowledgeSearchDiagnostics.dense_candidates }} 条 ·
          BM25 {{ knowledgeSearchDiagnostics.keyword_candidates }} 条 · 融合后 {{ knowledgeSearchDiagnostics.fused_candidates }} 条
        </div>
        <div v-if="knowledgeSearchDiagnostics && knowledgeSearchDiagnostics.query_assessment" class="query-assessment">
          <div>
            <strong>策略建议（本次检索预览）：</strong>
            {{ knowledgeSearchDiagnostics.query_assessment.deep_recommended ? '建议进入 rag_deep' : '保持 rag_fast' }} ·
            复杂度 {{ queryComplexityLabel(knowledgeSearchDiagnostics.query_assessment.complexity) }} ·
            信息缺口 {{ queryGapLabel(knowledgeSearchDiagnostics.query_assessment.gap) }}
          </div>
          <div class="query-assessment-reasons">
            判定依据：{{ knowledgeSearchDiagnostics.query_assessment.reason_codes.map(queryReasonLabel).join('；') }}
          </div>
          <div v-if="knowledgeSearchDiagnostics.query_assessment.deep_recommended" class="query-assessment-action">
            如需执行改写、多查询合并和重排，请点击“深度分析回答”；普通混合检索不会暗中增加模型调用。
          </div>
        </div>
        <div v-if="knowledgeSearchResults.length" class="knowledge-search-results">
          <article v-for="(hit, index) in knowledgeSearchResults" :key="hit.evidence.id" class="evidence-card">
            <div class="evidence-title">
              <strong>#{{ index + 1 }} {{ hit.evidence.title }}</strong>
              <span>{{ hit.evidence.retrieval }} · RRF {{ hit.rrf_score.toFixed(4) }}</span>
            </div>
            <div class="evidence-location">
              v{{ hit.evidence.source_version }} · {{ hit.evidence.section || '未命名章节' }} ·
              L{{ hit.evidence.line_start }}-{{ hit.evidence.line_end }}
            </div>
            <pre>{{ hit.evidence.content }}</pre>
          </article>
        </div>
        <div v-else-if="knowledgeSearchDiagnostics && !searchingKnowledge" class="knowledge-search-empty">
          当前知识库没有召回相关证据；这只是检索结果，不会让聊天模型凭空回答。
        </div>
      </div>

      <section v-if="memoryPreviewOpen" class="memory-workbench">
        <div class="memory-workbench-header">
          <div>
            <strong>🧠 Context Assembler v2 · 三级记忆控制台</strong>
            <span>Working、用户确认的 Episodic、环境 Profile 都以 MySQL 为权威；Redis 仅作可重建缓存/索引</span>
          </div>
          <div>
            <button :disabled="loadingMemoryEvaluation" @click="toggleMemoryEvaluation">{{ memoryEvaluationOpen ? '收起记忆评测' : '查看记忆评测' }}</button>
            <button :disabled="loadingMemoryPreview" @click="loadMemoryPreview">刷新</button>
            <button :disabled="loadingMemoryPreview || !currentSessionId || tempSession" @click="rebuildWorkingMemory">从 MySQL 安全重建 Working</button>
          </div>
        </div>
        <div v-if="loadingMemoryPreview" class="memory-loading">正在读取当前用户的记忆边界...</div>
        <template v-else-if="memoryPreview || profileMemories">
          <section v-if="memoryEvaluationOpen" class="diagnostic-evaluation">
            <div v-if="loadingMemoryEvaluation" class="diagnostic-evaluation-loading">正在读取可追溯记忆评测报告...</div>
            <template v-else-if="memoryEvaluation">
              <div class="diagnostic-evaluation-title">
                <div><strong>三级记忆安全契约评测</strong><span>{{ memoryEvaluation.metrics.case_count }} 条 · {{ memoryEvaluation.dataset_version }}</span></div>
                <span :class="['evaluation-gate', memoryEvaluation.technical_gates_passed ? 'passed' : 'failed']">
                  {{ memoryEvaluation.technical_gates_passed ? '技术门通过' : '技术门未通过' }}
                </span>
              </div>
              <div class="diagnostic-evaluation-grid">
                <div><strong>{{ metricPercent(memoryEvaluation.metrics.relevant_memory_recall) }}</strong><span>相关记忆召回</span></div>
                <div><strong>{{ metricPercent(memoryEvaluation.metrics.stale_wrong_injection_rate) }}</strong><span>过期/错误注入率</span></div>
                <div><strong>{{ memoryEvaluation.metrics.deleted_memory_recall }}</strong><span>删除后召回数</span></div>
                <div><strong>{{ memoryEvaluation.metrics.cross_principal_leakage }}</strong><span>跨用户/租户泄漏</span></div>
                <div><strong>{{ metricPercent(memoryEvaluation.metrics.context_budget_pass_rate) }}</strong><span>Token 预算遵守率</span></div>
                <div><strong>{{ metricPercent(memoryEvaluation.metrics.deterministic_replay_rate) }}</strong><span>确定性重放率</span></div>
              </div>
              <div class="evaluation-candidate-warning">
                <strong>候选报告，不是正式基线：</strong>
                <span v-for="limitation in memoryEvaluation.limitations" :key="limitation">{{ limitation }}</span>
              </div>
              <small>{{ memoryEvaluation.evaluator_version }} · 报告 SHA-256 {{ memoryEvaluation.report_sha256.slice(0, 16) }}… · 人工复核 {{ memoryEvaluation.human_reviewed ? '完成' : '未完成' }}</small>
            </template>
          </section>
          <div v-if="memoryPreview" class="memory-stats">
            <span :class="['memory-cache-badge', `cache-${memoryPreview.window.cache_status}`]">
              {{ memoryCacheLabel(memoryPreview.window.cache_status) }}
            </span>
            <span>热窗口 {{ memoryPreview.window.messages.length }}/{{ memoryPreview.window.window_limit }} 条</span>
            <span>TTL {{ Math.round(memoryPreview.window.cache_ttl_seconds / 3600) }} 小时</span>
            <span>预算 {{ memoryPreview.context.estimated_tokens }}/{{ memoryPreview.context.budget_tokens }} Token</span>
            <span>裁剪 {{ memoryPreview.context.dropped_by_budget }} 项</span>
            <span>估算 Token 降幅 {{ metricPercent(memoryPreview.context.token_reduction_ratio) }}</span>
            <span v-if="memoryPreview.profile_recall">
              Profile {{ memoryPreview.profile_recall.status === 'hit' ? `命中 ${memoryPreview.profile_recall.returned} 条` : memoryPreview.profile_recall.status === 'no_match' ? '无相关事实' : '降级未使用' }} · {{ memoryPreview.profile_recall.policy_version }}
            </span>
          </div>
          <div class="memory-boundary">
            Working 已参与聊天上下文；Episodic 只接收用户明确确认的解决案例；Profile 自动提取结果先是候选，只有你确认/更正后才成为可召回环境事实。
          </div>
          <details v-if="memoryPreview" open>
            <summary>按当前历史和预算重建的上下文预览（{{ memoryPreview.context.included.length }}）</summary>
            <ol class="memory-context-items">
              <li v-for="(item, index) in memoryPreview.context.included" :key="`${item.kind}-${index}`">
                <strong>{{ memoryContextKindLabel(item.kind) }}</strong>
                <span>{{ item.required ? '不可压缩' : '按预算纳入' }} · 约 {{ item.estimated_tokens }} Token</span>
                <p>{{ item.content }}</p>
              </li>
            </ol>
          </details>
          <div v-else class="memory-no-session">当前没有聊天会话，因此只展示跨会话 Profile Memory；选择历史会话后可查看 Working Memory。</div>
          <section v-if="profileMemories" class="profile-memory-panel">
            <div class="profile-memory-summary">
              <strong>Profile Memory · 环境事实</strong>
              <span>已确认 {{ profileMemories.active_count }} · 待确认 {{ profileMemories.candidate_count }} · 冲突 {{ profileMemories.conflict_count }}</span>
            </div>
            <p>候选和冲突事实不会自动进入模型上下文。确认时可直接改值；更正会生成新版本并取代同键旧值。</p>
            <div v-if="profileMemories.items.length" class="profile-memory-list">
              <article v-for="item in profileMemories.items" :key="item.id" :class="`profile-${item.status}`">
                <div class="profile-memory-card-header">
                  <strong>{{ profileKeyLabel(item.key) }}</strong>
                  <span>{{ profileStatusLabel(item.status) }} · 置信度 {{ Math.round(item.confidence * 100) }}% · v{{ item.version }}</span>
                </div>
                <input v-model="profileDrafts[item.id]" maxlength="256" :aria-label="`${profileKeyLabel(item.key)}的环境记忆值`" />
                <div class="profile-memory-actions">
                  <small>来源：{{ profileSourceLabel(item.source_type) }}{{ item.expires_at ? ` · 有效至 ${new Date(item.expires_at).toLocaleDateString()}` : ' · 长期有效' }}</small>
                  <div>
                    <button :disabled="profileMemoryBusy === item.id || !profileDrafts[item.id]?.trim()" @click="correctProfileMemory(item)">
                      {{ item.status === 'active' ? '保存更正' : '确认并启用' }}
                    </button>
                    <button class="profile-delete-btn" :disabled="profileMemoryBusy === item.id" @click="deleteProfileMemory(item)">删除</button>
                  </div>
                </div>
              </article>
            </div>
            <div v-else class="memory-no-session">尚无环境记忆。故障诊断中明确出现 OS、Go、部署方式、云厂商、Redis/MySQL 版本后会生成待确认候选。</div>
          </section>
        </template>
      </section>

      <section v-if="diagnosticMode" class="diagnostic-workbench">
        <div class="diagnostic-workbench-header">
          <div>
            <strong>🩺 可恢复故障诊断</strong>
            <span>只形成有证据的假设和只读验证步骤，不执行修复命令</span>
            <span v-if="diagnosticRecovered" class="diagnostic-recovered">已从服务端持久化检查点恢复</span>
          </div>
          <div class="diagnostic-actions">
            <button v-if="activeDiagnosticRun" type="button" :disabled="loadingContextCompression" @click="toggleContextCompression">
              {{ contextCompressionOpen ? '收起上下文工程' : '查看上下文工程' }}
            </button>
            <button type="button" :disabled="loadingDiagnosticEvaluation" @click="toggleDiagnosticEvaluation">
              {{ diagnosticEvaluationOpen ? '收起评测' : '查看评测' }}
            </button>
            <button v-if="activeDiagnosticRun && !isDiagnosticTerminal(activeDiagnosticRun.run.state)" :disabled="loading" @click="cancelDiagnosticRun">取消运行</button>
            <button :disabled="loading" @click="resetDiagnosticRun">新建诊断</button>
          </div>
        </div>
        <section v-if="diagnosticEvaluationOpen" class="diagnostic-evaluation">
          <div v-if="loadingDiagnosticEvaluation" class="diagnostic-evaluation-loading">正在读取可追溯评测报告...</div>
          <template v-else-if="diagnosticEvaluation">
            <div class="diagnostic-evaluation-title">
              <div>
                <strong>DiagnosticAgent 技术候选评测</strong>
                <span>{{ diagnosticEvaluation.metrics.case_count }} 条 · {{ diagnosticEvaluation.dataset_version }}</span>
              </div>
              <span :class="['evaluation-gate', diagnosticEvaluation.technical_gates_passed ? 'passed' : 'failed']">
                {{ diagnosticEvaluation.technical_gates_passed ? '技术门通过' : '技术门未通过' }}
              </span>
            </div>
            <div class="diagnostic-evaluation-grid">
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.root_cause_top3_recall) }}</strong><span>根因 Top-3 Recall</span></div>
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.necessary_step_coverage) }}</strong><span>必要步骤覆盖率</span></div>
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.verification_action_accuracy) }}</strong><span>验证动作准确率</span></div>
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.clarification_accuracy) }}</strong><span>澄清判断准确率</span></div>
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.premature_certainty_rate) }}</strong><span>过早确认率</span></div>
              <div><strong>{{ metricPercent(diagnosticEvaluation.metrics.dangerous_action_rate) }}</strong><span>危险动作率</span></div>
            </div>
            <div class="evaluation-candidate-warning">
              <strong>候选报告，不是正式基线：</strong>
              <span v-for="limitation in diagnosticEvaluation.limitations" :key="limitation">{{ limitation }}</span>
            </div>
            <details>
              <summary>查看分类覆盖与可追溯版本</summary>
              <div class="evaluation-category-list">
                <span v-for="(score, category) in diagnosticEvaluation.metrics.category_root_cause_recall" :key="category">
                  {{ category }} {{ metricPercent(score) }}
                </span>
              </div>
              <small>
                {{ diagnosticEvaluation.evaluator_version }} · 报告 SHA-256 {{ diagnosticEvaluation.report_sha256.slice(0, 16) }}… ·
                人工复核 {{ diagnosticEvaluation.human_reviewed ? '完成' : '未完成' }} ·
                基线资格 {{ diagnosticEvaluation.baseline_eligible ? '具备' : '不具备' }}
              </small>
            </details>
          </template>
        </section>
        <section v-if="contextCompressionOpen" class="diagnostic-evaluation">
          <div v-if="loadingContextCompression" class="diagnostic-evaluation-loading">正在从持久化 Checkpoint 重建结构化上下文...</div>
          <template v-else-if="contextCompression">
            <div class="diagnostic-evaluation-title">
              <div>
                <strong>Context Engineering · 结构化压缩</strong>
                <span>{{ contextCompression.run_state }} v{{ contextCompression.state_version }} · {{ contextCompression.assembler_version }}</span>
              </div>
              <span :class="['evaluation-gate', contextCompression.context.over_budget ? 'failed' : 'passed']">
                {{ contextCompression.context.over_budget ? '预算超限' : '预算内组装' }}
              </span>
            </div>
            <div class="diagnostic-evaluation-grid">
              <div><strong>{{ contextCompression.source_tokens }}</strong><span>压缩前估算 Token</span></div>
              <div><strong>{{ contextCompression.assembled_tokens }}</strong><span>组装后估算 Token</span></div>
              <div><strong>{{ metricPercent(contextCompression.token_reduction_ratio) }}</strong><span>Token 降幅</span></div>
              <div><strong>{{ metricPercent(contextCompression.retention.constraints.rate) }}</strong><span>约束保留率</span></div>
              <div><strong>{{ metricPercent(contextCompression.retention.confirmed_facts.rate) }}</strong><span>确认事实保留率</span></div>
              <div><strong>{{ metricPercent(contextCompression.retention.open_questions.rate) }}</strong><span>未决问题保留率</span></div>
            </div>
            <template v-if="contextEvaluation">
              <div class="diagnostic-evaluation-title">
                <div>
                  <strong>12 条成对压缩候选集</strong>
                  <span>answer / clarify / refuse / resume 各 3 条</span>
                </div>
                <span :class="['evaluation-gate', contextEvaluation.technical_gates_passed ? 'passed' : 'failed']">
                  {{ contextEvaluation.technical_gates_passed ? '技术门通过' : '技术门未通过' }}
                </span>
              </div>
              <div class="diagnostic-evaluation-grid">
                <div><strong>{{ metricPercent(contextEvaluation.metrics.constraint_retention) }}</strong><span>约束保留</span></div>
                <div><strong>{{ metricPercent(contextEvaluation.metrics.confirmed_fact_retention) }}</strong><span>确认事实保留</span></div>
                <div><strong>{{ metricPercent(contextEvaluation.metrics.open_question_retention) }}</strong><span>未决项保留</span></div>
                <div><strong>{{ metricPercent(contextEvaluation.metrics.next_action_retention) }}</strong><span>下一动作保留</span></div>
                <div><strong>{{ metricPercent(contextEvaluation.metrics.average_token_reduction) }}</strong><span>平均 Token 降幅</span></div>
                <div><strong>{{ contextEvaluation.metrics.over_budget_cases }}</strong><span>预算超限用例</span></div>
              </div>
            </template>
            <details>
              <summary>查看可校验结构化摘要（不是隐藏思维链）</summary>
              <div class="context-summary-fields">
                <p><strong>目标：</strong>{{ contextCompression.structured_summary.goal || '无' }}</p>
                <p><strong>约束：</strong>{{ (contextCompression.structured_summary.constraints || []).join('；') || '无' }}</p>
                <p><strong>已确认事实：</strong>{{ formattedFacts(contextCompression.structured_summary.confirmed_facts) }}</p>
                <p><strong>未决问题：</strong>{{ (contextCompression.structured_summary.open_questions || []).join('；') || '无' }}</p>
                <p><strong>已完成步骤：</strong>{{ (contextCompression.structured_summary.completed_steps || []).join('；') || '无' }}</p>
                <p><strong>失败步骤：</strong>{{ (contextCompression.structured_summary.failed_steps || []).join('；') || '无' }}</p>
                <p><strong>证据引用：</strong>{{ (contextCompression.structured_summary.evidence_refs || []).join('、') || '无' }}</p>
                <p><strong>下一动作：</strong>{{ contextCompression.structured_summary.next_action || '无' }}</p>
              </div>
            </details>
            <div class="evaluation-candidate-warning">
              <strong>口径说明：</strong>
              <span v-for="limitation in contextCompression.limitations" :key="limitation">{{ limitation }}</span>
            </div>
          </template>
        </section>
        <div v-if="!activeDiagnosticRun" class="diagnostic-empty">
          <span v-if="restoringDiagnosticRun">正在恢复上次诊断 Run...</span>
          <span v-else>在下方输入故障现象和脱敏日志。系统会展示状态机、公开步骤、预算、假设、证据与验证方法。</span>
        </div>
        <template v-else>
          <div class="diagnostic-run-summary">
            <span :class="['run-state-badge', `state-${activeDiagnosticRun.run.state.toLowerCase()}`]">
              {{ diagnosticStateLabel(activeDiagnosticRun.run.state) }}
            </span>
            <span>Run {{ activeDiagnosticRun.run.run_id.slice(0, 8) }}</span>
            <span>状态版本 v{{ activeDiagnosticRun.run.state_version }}</span>
            <span>策略 {{ activeDiagnosticRun.run.strategy }} · {{ activeDiagnosticRun.run.policy_version }}</span>
            <span>
              预算：轮次 {{ activeDiagnosticRun.run.budget.used_iterations }}/{{ activeDiagnosticRun.run.budget.max_iterations }} ·
              工具 {{ activeDiagnosticRun.run.budget.used_tool_calls }}/{{ activeDiagnosticRun.run.budget.max_tool_calls }} ·
              输入 {{ activeDiagnosticRun.run.budget.used_input_tokens }}/{{ activeDiagnosticRun.run.budget.max_input_tokens }} Token
            </span>
          </div>
          <div v-if="activeDiagnosticRun.run.state === 'WAITING_USER'" class="diagnostic-waiting">
            <strong>需要补充信息后才能继续：</strong>
            {{ (activeDiagnosticRun.checkpoint?.open_questions || []).join('；') }}
            <div>请直接在下方输入补充信息；恢复请求会携带当前状态版本，陈旧页面不会覆盖新状态。</div>
          </div>
          <details class="diagnostic-steps" open>
            <summary>公开执行轨迹（不包含隐藏思维链）</summary>
            <ol>
              <li v-for="step in activeDiagnosticRun.steps" :key="`${step.step_id}-${step.attempt}`">
                <strong>{{ step.public_summary || step.kind }}</strong>
                <span>{{ step.reason_code }} · 状态版本 v{{ step.state_version }}</span>
              </li>
            </ol>
          </details>
          <section
            v-if="activeDiagnosticRun.result?.case_memory_status"
            :class="['case-memory-recall', `case-${activeDiagnosticRun.result.case_memory_status}`]"
          >
            <div class="case-memory-header">
              <strong>🧠 Episodic Memory · 相似已解决案例</strong>
              <span v-if="activeDiagnosticRun.result.case_memory_status === 'hit'">
                命中 {{ activeDiagnosticRun.result.similar_incidents.length }} 条 · TopK≤3 · {{ activeDiagnosticRun.result.case_memory_policy }}
              </span>
              <span v-else-if="activeDiagnosticRun.result.case_memory_status === 'no_match'">已检索 · 无高相似案例</span>
              <span v-else>召回暂不可用 · 当前诊断不受影响</span>
            </div>
            <p class="case-memory-disclaimer">
              历史案例只提供排查经验，不是当前故障证据；不会改变本次根因假设、置信度或证据门判断。
            </p>
            <div v-if="activeDiagnosticRun.result.case_memory_status === 'hit'" class="case-memory-list">
              <article v-for="item in activeDiagnosticRun.result.similar_incidents" :key="item.incident_id">
                <div>
                  <strong>相似度 {{ Math.round(item.score * 100) }}%</strong>
                  <span>匹配错误特征：{{ item.matched_error_signatures.join('、') }}</span>
                  <span v-if="item.matched_components.length">匹配组件：{{ item.matched_components.join('、') }}</span>
                </div>
                <details>
                  <summary>查看历史根因与已验证解决办法</summary>
                  <p><strong>当时现象：</strong>{{ item.symptom }}</p>
                  <p><strong>当时根因：</strong>{{ item.root_cause }}</p>
                  <p><strong>当时解决：</strong>{{ item.resolution }}</p>
                  <small>案例 {{ item.incident_id.slice(0, 8) }} · 用户明确确认后才进入记忆</small>
                </details>
              </article>
            </div>
          </section>
          <div v-if="activeDiagnosticRun.result?.hypotheses?.length" class="diagnostic-hypotheses">
            <article v-for="hypothesis in activeDiagnosticRun.result.hypotheses" :key="hypothesis.id">
              <div class="hypothesis-header">
                <strong>{{ hypothesis.cause }}</strong>
                <span>置信度 {{ Math.round(hypothesis.confidence * 100) }}% · 待验证假设</span>
              </div>
              <p>{{ hypothesis.rationale }}</p>
              <div><strong>证据：</strong>{{ hypothesis.evidence.map(item => item.summary).join('；') }}</div>
              <ol>
                <li v-for="step in hypothesis.verification_steps" :key="step.id">
                  {{ step.instruction }}<br>
                  <small>预期：{{ step.expected_observation }}；否则：{{ step.failure_meaning }}</small>
                </li>
              </ol>
              <button
                v-if="activeDiagnosticRun.run.state === 'SUCCEEDED' && !confirmedResolution"
                class="resolution-preview-btn"
                :disabled="loadingResolution"
                @click="previewResolution(hypothesis.id)"
              >
                {{ loadingResolution ? '生成确认预览中...' : '这个假设已验证，预览案例记忆' }}
              </button>
            </article>
          </div>
          <section v-if="resolutionProposal && !confirmedResolution" class="resolution-proposal">
            <div class="resolution-proposal-header">
              <div>
                <strong>🧩 Action Proposal · 写入案例记忆前预览</strong>
                <span>预览不会写数据库；确认后才会事务写入反馈、案例和 Outbox</span>
              </div>
              <button @click="closeResolutionProposal">关闭</button>
            </div>
            <div class="resolution-proposal-content">
              <p><strong>故障现象：</strong>{{ resolutionProposal.symptom }}</p>
              <p><strong>你将确认的根因：</strong>{{ resolutionProposal.proposed_root_cause }}</p>
              <p><strong>已有证据：</strong>{{ resolutionProposal.evidence.map(item => item.summary).join('；') }}</p>
            </div>
            <label class="resolution-input-label">
              <span>请填写你实际执行且已验证有效的解决办法（不会自动执行）：</span>
              <textarea v-model="resolutionText" maxlength="1000" rows="3" placeholder="例如：修正后端容器的 Redis 主机名并重启，随后 PING 返回 PONG、接口恢复。"></textarea>
            </label>
            <label class="resolution-confirm-check">
              <input v-model="resolutionAcknowledged" type="checkbox" />
              我确认该办法已经在真实环境验证有效，并同意将脱敏结果作为我的已解决案例
            </label>
            <button
              class="resolution-confirm-btn"
              :disabled="loadingResolution || !resolutionAcknowledged || resolutionText.trim().length < 5"
              @click="confirmResolution"
            >
              {{ loadingResolution ? '确认写入中...' : '确认解决并写入 Episodic Memory' }}
            </button>
          </section>
          <section v-if="confirmedResolution" class="confirmed-resolution">
            <div class="confirmed-resolution-header">
              <strong>✅ 用户已确认的解决案例</strong>
              <div class="confirmed-resolution-actions">
                <span :class="['incident-index-badge', `index-${confirmedResolution.index_status}`]">
                  {{ incidentIndexLabel(confirmedResolution.index_status) }}
                </span>
                <button :disabled="loadingResolution" @click="loadConfirmedResolution">刷新索引状态</button>
              </div>
            </div>
            <p><strong>根因：</strong>{{ confirmedResolution.root_cause }}</p>
            <p><strong>实际解决：</strong>{{ confirmedResolution.resolution }}</p>
            <small>案例 {{ confirmedResolution.id.slice(0, 8) }} · 仅当前用户可召回 · 未确认假设不会进入索引</small>
          </section>
        </template>
      </section>

      <div class="chat-messages" ref="messagesRef">
        <div
          v-for="(message, index) in currentMessages"
          :key="index"
          :class="['message', message.role === 'user' ? 'user-message' : 'ai-message']"
        >
          <div class="message-header">
            <b>{{ message.role === 'user' ? '你' : 'AI' }}:</b>
            <button v-if="message.role === 'assistant'" class="tts-btn" @click="playTTS(message.content)">🔊</button>
            <span v-if="message.meta && message.meta.status === 'streaming'" class="streaming-indicator"> ··</span>
          </div>
          <div class="message-content" v-html="renderMarkdown(message.content)"></div>
          <div v-if="message.role === 'assistant' && message.meta && message.meta.traceId" class="routing-meta">
            <div>实际路由 · {{ message.meta.strategy }} · {{ message.meta.policyVersion }} · Trace {{ message.meta.traceId.slice(0, 8) }}</div>
            <div v-if="message.meta.intentShadow" class="shadow-intent-meta">
              影子判断 · {{ intentLabel(message.meta.intentShadow.intent) }} ·
              {{ intentStageLabel(message.meta.intentShadow.final_stage) }} ·
              {{ Math.round((message.meta.intentShadow.confidence || 0) * 100) }}% · 不切流
              <span v-if="message.meta.intentShadow.needs_clarify"> · 建议澄清</span>
            </div>
          </div>
          <div v-if="message.role === 'assistant' && message.meta && message.meta.citations && message.meta.citations.length" class="chat-citations">
            <span v-for="(citation, citationIndex) in message.meta.citations" :key="citation.citation_id">
              [{{ citationIndex + 1 }}] {{ citation.document }} · v{{ citation.version }} ·
              {{ citation.section || '未命名章节' }} · L{{ citation.line_start }}-{{ citation.line_end }}
            </span>
          </div>
        </div>
      </div>

      <div class="chat-input">
        <div class="input-wrapper">
          <textarea
            v-model="inputMessage"
            :placeholder="diagnosticMode && activeDiagnosticRun?.run?.state === 'WAITING_USER' ? '补充上方要求的环境或日志信息，继续同一个 Run...' : '请输入项目问题、报错信息或排障目标...'"
            @keydown.enter.exact.prevent="sendMessage"
            :disabled="loading"
            ref="messageInput"
            rows="1"
          ></textarea>
        </div>
        <button
          type="button"
          :disabled="!inputMessage.trim() || loading"
          @click="sendMessage"
          class="send-btn"
        >
          {{ loading ? '发送中...' : '发送' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script>


import { ref, nextTick, computed, onMounted, onUnmounted } from 'vue'
import { ElMessage } from 'element-plus'
import api from '../utils/api'

export default {
  name: 'AIChat',
  setup() {

    const sessions = ref({})
    const currentSessionId = ref(null)
    const tempSession = ref(false)
    const currentMessages = ref([])
    const inputMessage = ref('')
    const loading = ref(false)
    const messagesRef = ref(null)
    const messageInput = ref(null)
    const isStreaming = ref(false)
    const knowledgeRequired = ref(false)
    const uploading = ref(false)
    const fileInput = ref(null)
    const versionFileInput = ref(null)
    const uploadingVersion = ref(false)
    const versionTargetDocumentId = ref('')
    const pendingVersionJob = ref(null)
    const rebuildingDocument = ref(false)
    const deletingDocument = ref(false)
    const knowledgeDocuments = ref([])
    const indexedKnowledgeDocuments = computed(() => knowledgeDocuments.value.filter(document => document.status === 'indexed'))
    const knowledgeSearchOpen = ref(false)
    const knowledgeQuery = ref('')
    const searchingKnowledge = ref(false)
    const knowledgeSearchResults = ref([])
    const knowledgeSearchDiagnostics = ref(null)
    const answeringKnowledge = ref(false)
    const answeringKnowledgeMode = ref('')
    const knowledgeAnswer = ref(null)
    const diagnosticMode = ref(false)
    const activeDiagnosticRun = ref(null)
    const diagnosticRecovered = ref(false)
    const restoringDiagnosticRun = ref(false)
    const diagnosticEvaluationOpen = ref(false)
    const loadingDiagnosticEvaluation = ref(false)
    const diagnosticEvaluation = ref(null)
    const contextCompressionOpen = ref(false)
    const loadingContextCompression = ref(false)
    const contextCompression = ref(null)
    const contextEvaluation = ref(null)
    const memoryPreviewOpen = ref(false)
    const loadingMemoryPreview = ref(false)
    const memoryPreview = ref(null)
    const memoryEvaluationOpen = ref(false)
    const loadingMemoryEvaluation = ref(false)
    const memoryEvaluation = ref(null)
    const toolRuntimeOpen = ref(false)
    const loadingToolCatalog = ref(false)
    const toolCatalog = ref(null)
    const invokingTool = ref(false)
    const toolResult = ref(null)
    const profileMemories = ref(null)
    const profileDrafts = ref({})
    const profileMemoryBusy = ref('')
    const resolutionProposal = ref(null)
    const resolutionText = ref('')
    const resolutionAcknowledged = ref(false)
    const loadingResolution = ref(false)
    const confirmedResolution = ref(null)
    const diagnosticRunStorageKey = 'gopherai.active-diagnostic-run-v1'
    const diagnosticModeStorageKey = 'gopherai.diagnostic-mode-v1'
    let knowledgePollTimer = null

    const renderMarkdown = (text) => {
      if (!text && text !== '') return ''
      return String(text)
        .replace(/\*\*(.*?)\*\*/g, '<strong>$1</strong>')
        .replace(/\*(.*?)\*/g, '<em>$1</em>')
        .replace(/`(.*?)`/g, '<code>$1</code>')
        .replace(/\n/g, '<br>')
    }

    const playTTS = async (text) => {
      try {
        // 创建TTS任务
        const createResponse = await api.post('/AI/chat/tts', { text })
        if (createResponse.data && createResponse.data.status_code === 1000 && createResponse.data.task_id) {
          const taskId = createResponse.data.task_id
          
          // 先等待5秒钟再开始轮询
          await new Promise(resolve => setTimeout(resolve, 5000))
          
          // 轮询查询任务结果
          const maxAttempts = 30
          const pollInterval = 2000
          let attempts = 0
          
          const pollResult = async () => {
            const queryResponse = await api.get('/AI/chat/tts/query', { params: { task_id: taskId } })
            
            if (queryResponse.data && queryResponse.data.status_code === 1000) {
              const taskStatus = queryResponse.data.task_status
                
              if (taskStatus === 'Success' && queryResponse.data.task_result) {
                // 任务完成，播放音频
                // 后端返回的 task_result 是直接的 URL 字符串
                const audio = new Audio(queryResponse.data.task_result)
                audio.play()
                return true
              } else if (taskStatus === 'Running' ||taskStatus === 'Created' ) {
                // 任务进行中，继续轮询
                attempts++
                if (attempts < maxAttempts) {
                  await new Promise(resolve => setTimeout(resolve, pollInterval))
                  return await pollResult()
                } else {
                  ElMessage.error('语音合成超时')
                  return true
                }
              } else {
                // 其他状态（如失败）
                ElMessage.error('语音合成失败')
                return true
              }
            }
            
            attempts++
            if (attempts < maxAttempts) {
              await new Promise(resolve => setTimeout(resolve, pollInterval))
              return await pollResult()
            } else {
              ElMessage.error('语音合成超时')
              return true
            }
          }
          
          await pollResult()
        } else {
          ElMessage.error('无法创建语音合成任务')
        }
      } catch (error) {
        console.error('TTS error:', error)
        ElMessage.error('请求语音接口失败')
      }
    }

    const loadSessions = async () => {
      try {
        const response = await api.get('/AI/chat/sessions')
        if (response.data && response.data.status_code === 1000 && Array.isArray(response.data.sessions)) {
          const sessionMap = {}
          response.data.sessions.forEach(s => {
            const sid = String(s.sessionId)
            sessionMap[sid] = {
              id: sid,
              name: s.name || `会话 ${sid}`,
              messages: [] // lazy load
            }
          })
          sessions.value = sessionMap
        }
      } catch (error) {
        console.error('Load sessions error:', error)
      }
    }

    const createNewSession = () => {
      currentSessionId.value = 'temp'
      tempSession.value = true
      currentMessages.value = []
      // focus input
      nextTick(() => {
        if (messageInput.value) messageInput.value.focus()
      })
    }

    const switchSession = async (sessionId) => {
      if (!sessionId) return
      currentSessionId.value = String(sessionId)
      tempSession.value = false

      // lazy load history if not present
      if (!sessions.value[sessionId].messages || sessions.value[sessionId].messages.length === 0) {
        try {
          const response = await api.post('/AI/chat/history', { sessionId: currentSessionId.value })
          if (response.data && response.data.status_code === 1000 && Array.isArray(response.data.history)) {
            const messages = response.data.history.map(item => ({
              role: item.is_user ? 'user' : 'assistant',
              content: item.content
            }))
            sessions.value[sessionId].messages = messages
          }
        } catch (err) {
          console.error('Load history error:', err)
        }
      }


      currentMessages.value = [...(sessions.value[sessionId].messages || [])]
      if (memoryPreviewOpen.value) await loadMemoryPreview()
      await nextTick()
      scrollToBottom()
    }

    const syncHistory = async () => {
      if (!currentSessionId.value || tempSession.value) {
        ElMessage.warning('请选择已有会话进行同步')
        return
      }
      try {
        const response = await api.post('/AI/chat/history', { sessionId: currentSessionId.value })
        if (response.data && response.data.status_code === 1000 && Array.isArray(response.data.history)) {
          const messages = response.data.history.map(item => ({
            role: item.is_user ? 'user' : 'assistant',
            content: item.content
          }))
          sessions.value[currentSessionId.value].messages = messages
          currentMessages.value = [...messages]
          await nextTick()
          scrollToBottom()
        } else {
          ElMessage.error('无法获取历史数据')
        }
      } catch (err) {
        console.error('Sync history error:', err)
        ElMessage.error('请求历史数据失败')
      }
    }


    const sendMessage = async () => {
      if (!inputMessage.value || !inputMessage.value.trim()) {
        ElMessage.warning('请输入消息内容')
        return
      }

      const userMessage = {
        role: 'user',
        content: inputMessage.value
      }
      const currentInput = inputMessage.value
      inputMessage.value = ''


      currentMessages.value.push(userMessage)
      await nextTick()
      scrollToBottom()

      try {
        loading.value = true
        if (diagnosticMode.value) {
          await handleDiagnostic(currentInput)
        } else if (isStreaming.value) {

          await handleStreaming(currentInput)
        } else {

          await handleNormal(currentInput)
        }
      } catch (err) {
        console.error('Send message error:', err)
        ElMessage.error('发送失败，请重试')

        if (!tempSession.value && currentSessionId.value && sessions.value[currentSessionId.value] && sessions.value[currentSessionId.value].messages) {

          const sessionArr = sessions.value[currentSessionId.value].messages
          if (sessionArr && sessionArr.length) sessionArr.pop()
        }
        currentMessages.value.pop()
      } finally {
        if (!isStreaming.value) {
          loading.value = false
        }
        await nextTick()
        scrollToBottom()
        if (memoryPreviewOpen.value && currentSessionId.value && !tempSession.value) await loadMemoryPreview()
      }
    }


    async function handleStreaming(question) {
      const aiMessage = {
        role: 'assistant',
        content: '',
        meta: { status: 'streaming', strategy: '固定路由', policyVersion: '加载中', traceId: '', intentShadow: null }
      }
      const aiMessageIndex = currentMessages.value.length
      currentMessages.value.push(aiMessage)

      if (!tempSession.value && currentSessionId.value && sessions.value[currentSessionId.value]) {
        if (!sessions.value[currentSessionId.value].messages) sessions.value[currentSessionId.value].messages = []
        sessions.value[currentSessionId.value].messages.push({ role: 'user', content: question })
        sessions.value[currentSessionId.value].messages.push({ role: 'assistant', content: '' })
      }

      const headers = {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${localStorage.getItem('token') || ''}`
      }
      const body = { message: question, knowledge_required: knowledgeRequired.value }
      if (!tempSession.value) body.session_id = currentSessionId.value

      try {
        const response = await fetch('/api/chat/auto/stream', {
          method: 'POST',
          headers,
          body: JSON.stringify(body)
        })

        if (!response.ok) {
          const errorBody = await response.json().catch(() => ({}))
          throw new Error(errorBody.message || 'Network response was not ok')
        }

        const reader = response.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''

        const processEventBlock = (block) => {
          let eventType = 'message'
          const dataLines = []
          block.split('\n').forEach(line => {
            if (line.startsWith('event:')) eventType = line.slice(6).trim()
            if (line.startsWith('data:')) dataLines.push(line.slice(5).trim())
          })
          if (!dataLines.length) return
          let payload
          try {
            payload = JSON.parse(dataLines.join('\n'))
          } catch {
            throw new Error('服务端返回了无法识别的流式事件')
          }

          const message = currentMessages.value[aiMessageIndex]
          if (eventType === 'meta') {
            message.meta = {
              status: 'streaming',
              traceId: payload.trace_id || '',
              strategy: payload.strategy || '固定路由',
              policyVersion: payload.policy_version || '',
              intentShadow: payload.intent_shadow || null
            }
            if (payload.session_id && tempSession.value) {
              const newSessionId = String(payload.session_id)
              sessions.value[newSessionId] = {
                id: newSessionId,
                name: question.slice(0, 30) || '新会话',
                messages: currentMessages.value
              }
              currentSessionId.value = newSessionId
              tempSession.value = false
            }
          } else if (eventType === 'delta') {
            message.content += payload.text || ''
          } else if (eventType === 'citation') {
            message.meta.citations = [...(message.meta.citations || []), payload.citation]
          } else if (eventType === 'final') {
            message.meta.status = 'done'
          } else if (eventType === 'error') {
            throw new Error(payload.error?.message || '流式生成失败')
          }
          currentMessages.value = [...currentMessages.value]
        }

        // eslint-disable-next-line no-constant-condition
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          buffer = (buffer + decoder.decode(value, { stream: true })).replace(/\r\n/g, '\n')
          const blocks = buffer.split('\n\n')
          buffer = blocks.pop() || ''
          blocks.filter(Boolean).forEach(processEventBlock)
          await nextTick()
          scrollToBottom()
        }
        if (buffer.trim()) processEventBlock(buffer.trim())

        loading.value = false
        currentMessages.value[aiMessageIndex].meta.status = 'done'
        currentMessages.value = [...currentMessages.value]

        // 同步到 sessions 存储
        if (!tempSession.value && currentSessionId.value && sessions.value[currentSessionId.value]) {
          const sessMsgs = sessions.value[currentSessionId.value].messages
          if (Array.isArray(sessMsgs) && sessMsgs.length) {
            const lastIndex = sessMsgs.length - 1
            if (sessMsgs[lastIndex] && sessMsgs[lastIndex].role === 'assistant') {
              sessMsgs[lastIndex].content = currentMessages.value[aiMessageIndex].content
            }
          }
        }
      } catch (err) {
        console.error('Stream error:', err)
        loading.value = false
        currentMessages.value[aiMessageIndex].meta.status = 'error'
        currentMessages.value = [...currentMessages.value]
        ElMessage.error(err.message || '流式传输出错')
      }
    }

    const newClientRequestId = () => {
      if (window.crypto && typeof window.crypto.randomUUID === 'function') return window.crypto.randomUUID()
      return `web-${Date.now()}-${Math.random().toString(16).slice(2)}`
    }

    const metricPercent = (value) => `${((Number(value) || 0) * 100).toFixed(1)}%`

    const memoryCacheLabel = (status) => ({
      hit: 'Redis 热窗口命中',
      rebuilt_from_mysql: '已从 MySQL 重建 Redis',
      mysql_fallback_cache_unavailable: 'Redis 不可用，MySQL 降级'
    }[status] || status)

    const memoryContextKindLabel = (kind) => ({
      safety_rule: '系统安全规则',
      current_question: '当前问题',
      constraint: '用户明确约束',
      run_state: '当前 Run 状态',
      confirmed_fact: '已确认事实',
      open_question: '未决问题',
      evidence_ref: '证据引用',
      structured_summary: '结构化摘要',
      profile_memory: '已确认环境事实',
      working_message: '近期原始消息'
    }[kind] || kind)

    const profileKeyLabel = (key) => ({
      os: '操作系统',
      go_version: 'Go 版本',
      deployment_mode: '部署方式',
      cloud_provider: '云环境',
      redis_version: 'Redis 版本',
      mysql_version: 'MySQL 版本'
    }[key] || key)

    const profileStatusLabel = (status) => ({ active: '已确认', candidate: '待确认', conflicted: '存在冲突' }[status] || status)
    const profileSourceLabel = (source) => ({ diagnostic_observation: '诊断输入提取', user_corrected: '用户确认/更正' }[source] || source)

    const loadMemoryPreview = async () => {
      try {
        loadingMemoryPreview.value = true
        const profileResponse = await api.get('/memory/profiles')
        profileMemories.value = profileResponse.data
        profileDrafts.value = Object.fromEntries((profileResponse.data.items || []).map(item => [item.id, item.value]))
        if (currentSessionId.value && !tempSession.value) {
          const response = await api.get(`/memory/sessions/${encodeURIComponent(currentSessionId.value)}/context`, {
            params: { budget_tokens: 1024 }
          })
          memoryPreview.value = response.data
        } else {
          memoryPreview.value = null
        }
      } catch (error) {
        memoryPreviewOpen.value = false
        ElMessage.error(error.response?.data?.message || '上下文记忆暂时不可用')
      } finally {
        loadingMemoryPreview.value = false
      }
    }

    const correctProfileMemory = async (item) => {
      try {
        profileMemoryBusy.value = item.id
        await api.patch(`/memory/profiles/${encodeURIComponent(item.id)}`, {
          value: profileDrafts.value[item.id],
          expires_in_days: 180
        })
        await loadMemoryPreview()
        ElMessage.success('环境事实已由你确认并启用，旧值不会继续生效')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '环境记忆确认失败')
      } finally {
        profileMemoryBusy.value = ''
      }
    }

    const deleteProfileMemory = async (item) => {
      if (!window.confirm(`确认删除环境记忆“${profileKeyLabel(item.key)}：${item.value}”吗？`)) return
      try {
        profileMemoryBusy.value = item.id
        await api.delete(`/memory/profiles/${encodeURIComponent(item.id)}`)
        await loadMemoryPreview()
        ElMessage.success('环境记忆已从 MySQL 权威记录删除')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '环境记忆删除失败')
      } finally {
        profileMemoryBusy.value = ''
      }
    }

    const toggleMemoryPreview = async () => {
      memoryPreviewOpen.value = !memoryPreviewOpen.value
      if (memoryPreviewOpen.value) await loadMemoryPreview()
    }

    const rebuildWorkingMemory = async () => {
      if (!currentSessionId.value || tempSession.value) return
      try {
        loadingMemoryPreview.value = true
        const rebuildResponse = await api.post(`/memory/sessions/${encodeURIComponent(currentSessionId.value)}/rebuild`)
        await loadMemoryPreview()
        if (memoryPreview.value?.window && rebuildResponse.data?.window?.cache_status) {
          memoryPreview.value.window.cache_status = rebuildResponse.data.window.cache_status
        }
        ElMessage.success('已删除当前用户该会话的 Redis 缓存，并从 MySQL 权威消息重建')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '工作记忆重建失败')
      } finally {
        loadingMemoryPreview.value = false
      }
    }

    const toggleDiagnosticEvaluation = async () => {
      diagnosticEvaluationOpen.value = !diagnosticEvaluationOpen.value
      if (!diagnosticEvaluationOpen.value || diagnosticEvaluation.value || loadingDiagnosticEvaluation.value) return
      try {
        loadingDiagnosticEvaluation.value = true
        const response = await api.get('/evaluations/diagnostic/latest')
        diagnosticEvaluation.value = response.data
      } catch (error) {
        diagnosticEvaluationOpen.value = false
        ElMessage.error(error.response?.data?.message || '诊断评测报告暂时不可用')
      } finally {
        loadingDiagnosticEvaluation.value = false
      }
    }

    const toggleContextCompression = async () => {
      contextCompressionOpen.value = !contextCompressionOpen.value
      if (!contextCompressionOpen.value || loadingContextCompression.value) return
      if (!activeDiagnosticRun.value?.run?.run_id) {
        contextCompressionOpen.value = false
        return
      }
      try {
        loadingContextCompression.value = true
        const [contextResponse, evaluationResponse] = await Promise.all([
          api.get(`/agent-runs/${activeDiagnosticRun.value.run.run_id}/context-compression`, { params: { budget_tokens: 512 } }),
          api.get('/evaluations/context/latest').catch(() => null)
        ])
        contextCompression.value = contextResponse.data
        contextEvaluation.value = evaluationResponse?.data || null
      } catch (error) {
        contextCompressionOpen.value = false
        ElMessage.error(error.response?.data?.message || '结构化上下文暂时不可用')
      } finally {
        loadingContextCompression.value = false
      }
    }

    const formattedFacts = (facts) => {
      const entries = Object.entries(facts || {})
      if (!entries.length) return '无'
      return entries.sort(([left], [right]) => left.localeCompare(right)).map(([key, value]) => `${key}=${value}`).join('；')
    }

    const toggleToolRuntime = async () => {
      toolRuntimeOpen.value = !toolRuntimeOpen.value
      if (!toolRuntimeOpen.value || toolCatalog.value || loadingToolCatalog.value) return
      try {
        loadingToolCatalog.value = true
        const response = await api.get('/tools')
        toolCatalog.value = response.data
      } catch (error) {
        toolRuntimeOpen.value = false
        ElMessage.error(error.response?.data?.message || '工具注册表暂时不可用')
      } finally {
        loadingToolCatalog.value = false
      }
    }

    const invokeGovernedTool = async (toolName, argumentsPayload = {}) => {
      try {
        invokingTool.value = true
        toolResult.value = null
        const response = await api.post('/tools/invoke', { tool_name: toolName, arguments: argumentsPayload, intent: 'tool_task' })
        toolResult.value = response.data
        ElMessage.success('部署清单已通过完整治理链路返回')
      } catch (error) {
        toolResult.value = error.response?.data || { status: 'error', error_code: 'TOOL_CONSOLE_REQUEST_FAILED', retryable: true }
        ElMessage.error(toolResult.value?.message || `工具调用失败：${toolResult.value?.error_code || '未知错误'}`)
      } finally {
        invokingTool.value = false
      }
    }

    const formatToolData = (data) => JSON.stringify(data, null, 2)

    const toggleMemoryEvaluation = async () => {
      memoryEvaluationOpen.value = !memoryEvaluationOpen.value
      if (!memoryEvaluationOpen.value || memoryEvaluation.value || loadingMemoryEvaluation.value) return
      try {
        loadingMemoryEvaluation.value = true
        const response = await api.get('/evaluations/memory/latest')
        memoryEvaluation.value = response.data
      } catch (error) {
        memoryEvaluationOpen.value = false
        ElMessage.error(error.response?.data?.message || '记忆评测报告暂时不可用')
      } finally {
        loadingMemoryEvaluation.value = false
      }
    }

    const diagnosticStateLabel = (state) => ({
      RECEIVED: '已接收',
      CONTEXT_READY: '上下文就绪',
      PLANNED: '计划完成',
      RUNNING: '诊断执行中',
      WAITING_USER: '等待补充信息',
      SUCCEEDED: '已形成诊断假设',
      FAILED: '运行失败',
      CANCELLED: '已取消',
      BUDGET_EXCEEDED: '预算终止'
    }[state] || state)

    const isDiagnosticTerminal = (state) => ['SUCCEEDED', 'FAILED', 'CANCELLED', 'BUDGET_EXCEEDED'].includes(state)

    const incidentIndexLabel = (status) => ({
      pending: '等待可靠索引',
      indexed: 'RabbitMQ 异步索引完成',
      failed: '索引失败，可由 Outbox/重试恢复'
    }[status] || status)

    const previewResolution = async (hypothesisId) => {
      if (!activeDiagnosticRun.value?.run?.run_id) return
      try {
        loadingResolution.value = true
        const response = await api.post(`/agent-runs/${activeDiagnosticRun.value.run.run_id}/resolution-proposals`, {
          hypothesis_id: hypothesisId
        })
        resolutionProposal.value = response.data
        resolutionText.value = ''
        resolutionAcknowledged.value = false
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '解决案例预览失败')
      } finally {
        loadingResolution.value = false
      }
    }

    const closeResolutionProposal = () => {
      resolutionProposal.value = null
      resolutionText.value = ''
      resolutionAcknowledged.value = false
    }

    const loadConfirmedResolution = async () => {
      if (!activeDiagnosticRun.value?.run?.run_id) return
      try {
        const response = await api.get(`/agent-runs/${activeDiagnosticRun.value.run.run_id}/resolution`)
        confirmedResolution.value = response.data
        resolutionProposal.value = null
      } catch (error) {
        if (error.response?.status !== 404) ElMessage.warning(error.response?.data?.message || '已解决案例状态暂时不可用')
        confirmedResolution.value = null
      }
    }

    const confirmResolution = async () => {
      if (!resolutionProposal.value || !resolutionAcknowledged.value || resolutionText.value.trim().length < 5) return
      try {
        loadingResolution.value = true
        const response = await api.post(`/agent-runs/${resolutionProposal.value.run_id}/resolution-confirmations`, {
          hypothesis_id: resolutionProposal.value.hypothesis_id,
          resolution: resolutionText.value,
          client_request_id: newClientRequestId(),
          expected_state_version: resolutionProposal.value.expected_state_version
        })
        confirmedResolution.value = response.data.incident
        resolutionProposal.value = null
        ElMessage.success(response.data.created ? '已确认解决，案例正在通过 Outbox 异步建立索引' : '该确认已处理，没有重复写入')
        window.setTimeout(loadConfirmedResolution, 1500)
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '确认解决方案失败')
      } finally {
        loadingResolution.value = false
      }
    }

    const diagnosticMessage = (payload) => {
      const state = payload.run?.state
      const result = payload.result
      if (state === 'WAITING_USER') {
        const questions = payload.checkpoint?.open_questions || result?.missing_information?.map(item => item.question) || []
        return `诊断 Run 已暂停，当前证据不足。\n\n请补充：\n${questions.map((item, index) => `${index + 1}. ${item}`).join('\n')}`
      }
      if (state === 'CANCELLED') return '诊断 Run 已取消，后续步骤不会继续执行。'
      if (state === 'BUDGET_EXCEEDED') return `诊断 Run 已被护栏终止：${payload.run?.terminal_reason || '预算已耗尽'}。`
      const hypotheses = result?.hypotheses || []
      if (!hypotheses.length) return `诊断运行状态：${diagnosticStateLabel(state)}。`
      return `已形成 ${hypotheses.length} 个有证据的待验证假设。完整依据、只读验证步骤和公开执行轨迹见上方“可恢复故障诊断”面板。当前没有把任何假设标记为已确认根因。`
    }

    async function handleDiagnostic(message) {
      let response
      if (activeDiagnosticRun.value?.run?.state === 'WAITING_USER') {
        response = await api.post(`/agent-runs/${activeDiagnosticRun.value.run.run_id}/resume`, {
          message,
          client_request_id: newClientRequestId(),
          expected_state_version: activeDiagnosticRun.value.run.state_version
        })
      } else {
        response = await api.post('/agent-runs/diagnostics', {
          message,
          session_id: tempSession.value ? '' : currentSessionId.value,
          client_request_id: newClientRequestId()
        })
      }
      activeDiagnosticRun.value = response.data
      confirmedResolution.value = null
      resolutionProposal.value = null
      diagnosticRecovered.value = false
      rememberDiagnosticRun(response.data)
      currentMessages.value.push({
        role: 'assistant',
        content: diagnosticMessage(response.data),
        meta: {
          status: 'done',
          traceId: response.data.run?.trace_id || '',
          strategy: response.data.run?.strategy || 'diagnosis_standard',
          policyVersion: response.data.run?.policy_version || 'policy-diagnostic-v1',
          diagnosticRunId: response.data.run?.run_id || ''
        }
      })
    }

    const cancelDiagnosticRun = async () => {
      if (!activeDiagnosticRun.value?.run?.run_id) return
      try {
        loading.value = true
        const response = await api.post(`/agent-runs/${activeDiagnosticRun.value.run.run_id}/cancel`)
        activeDiagnosticRun.value = response.data
        rememberDiagnosticRun(response.data)
        ElMessage.success('诊断运行已取消')
      } catch (error) {
        ElMessage.error(error.response?.data?.message || '取消诊断运行失败')
      } finally {
        loading.value = false
      }
    }

    const resetDiagnosticRun = () => {
      activeDiagnosticRun.value = null
      contextCompressionOpen.value = false
      contextCompression.value = null
      contextEvaluation.value = null
      confirmedResolution.value = null
      resolutionProposal.value = null
      diagnosticRecovered.value = false
      sessionStorage.removeItem(diagnosticRunStorageKey)
      nextTick(() => messageInput.value?.focus())
    }

    const rememberDiagnosticRun = (payload) => {
      const runId = payload?.run?.run_id
      if (runId) sessionStorage.setItem(diagnosticRunStorageKey, runId)
    }

    const restoreDiagnosticRun = async () => {
      const runId = sessionStorage.getItem(diagnosticRunStorageKey)
      if (!runId || activeDiagnosticRun.value || restoringDiagnosticRun.value) return
      try {
        restoringDiagnosticRun.value = true
        const response = await api.get(`/agent-runs/${encodeURIComponent(runId)}`)
        activeDiagnosticRun.value = response.data
        diagnosticRecovered.value = true
        await loadConfirmedResolution()
      } catch (error) {
        if (error.response?.status === 404) sessionStorage.removeItem(diagnosticRunStorageKey)
        ElMessage.warning(error.response?.data?.message || '上次诊断 Run 暂时无法恢复，可稍后重新打开诊断模式')
      } finally {
        restoringDiagnosticRun.value = false
      }
    }

    const onDiagnosticModeChanged = async () => {
      sessionStorage.setItem(diagnosticModeStorageKey, diagnosticMode.value ? '1' : '0')
      if (diagnosticMode.value) {
        isStreaming.value = false
        knowledgeRequired.value = false
        await restoreDiagnosticRun()
      }
    }


    async function handleNormal(question) {
      if (tempSession.value) {
        const response = await api.post('/chat/auto', { message: question, knowledge_required: knowledgeRequired.value })
        if (response.data && response.data.session_id) {
          const sessionId = String(response.data.session_id)
          const aiMessage = {
            role: 'assistant',
            content: response.data.message || '',
            meta: {
              status: 'done', traceId: response.data.trace_id || '',
              strategy: response.data.strategy || '固定路由', policyVersion: response.data.policy_version || '',
              intentShadow: response.data.intent_shadow || null,
              citations: response.data.citations || [], needsUserInput: response.data.needs_user_input || false
            }
          }

          sessions.value[sessionId] = {
            id: sessionId,
            name: question.slice(0, 30) || '新会话',
            messages: [ { role: 'user', content: question }, aiMessage ]
          }
          currentSessionId.value = sessionId
          tempSession.value = false
          currentMessages.value = [...sessions.value[sessionId].messages]
        } else {
          ElMessage.error(response.data?.message || '发送失败')

          currentMessages.value.pop()
        }
      } else {

        const sessionMsgs = sessions.value[currentSessionId.value].messages

        sessionMsgs.push({ role: 'user', content: question })

        const response = await api.post('/chat/auto', {
          message: question,
          session_id: currentSessionId.value,
          knowledge_required: knowledgeRequired.value
        })
        if (response.data && response.data.session_id) {
          const aiMessage = {
            role: 'assistant',
            content: response.data.message || '',
            meta: {
              status: 'done', traceId: response.data.trace_id || '',
              strategy: response.data.strategy || '固定路由', policyVersion: response.data.policy_version || '',
              intentShadow: response.data.intent_shadow || null,
              citations: response.data.citations || [], needsUserInput: response.data.needs_user_input || false
            }
          }
          sessionMsgs.push(aiMessage)
          currentMessages.value = [...sessionMsgs]
        } else {
          ElMessage.error(response.data?.message || '发送失败')
          sessionMsgs.pop() // rollback
          currentMessages.value.pop()
        }
      }
    }


    const intentLabel = (intent) => ({
      project_qa: '项目知识问答',
      troubleshooting: '故障排查',
      doc_task: '文档任务',
      tool_task: '受治理的操作任务',
      follow_up: '上下文追问',
      general: '通用对话'
    }[intent] || '未知意图')

    const intentStageLabel = (stage) => ({
      pattern: '规则高置信命中',
      prototype: '语义原型匹配',
      llm: '结构化模型判定',
      degraded_clarification: '安全降级',
      unavailable: '识别器暂不可用'
    }[stage] || '未知阶段')

    const scrollToBottom = () => {
      if (messagesRef.value) {
        try {
          messagesRef.value.scrollTop = messagesRef.value.scrollHeight
        } catch (e) {
          // ignore
        }
      }
    }

    const triggerFileUpload = () => {
      if (fileInput.value) {
        fileInput.value.click()
      }
    }

    const documentStatusLabel = (status) => {
      const labels = {
        uploaded: '已接收，等待索引',
        parsing: '正在解析与索引',
        indexed: '索引完成，可检索',
        failed: '索引失败'
      }
      return labels[status] || status
    }

  const jobStatusLabel = (status) => {
    const labels = {
    queued: '等待索引',
    processing: '正在建立索引',
    retrying: '索引重试中',
    completed: '已切换为活动版本',
    failed: '索引失败'
    }
    return labels[status] || status
  }

    const retrievalModeLabel = (mode) => {
      const labels = {
        hybrid: 'Dense + BM25 混合检索',
        dense_only: 'Dense 降级检索',
        bm25_only: 'BM25 降级检索'
      }
      return labels[mode] || '检索状态未知'
    }

    const queryComplexityLabel = (complexity) => ({ simple: '简单', complex: '复杂' }[complexity] || complexity)

    const queryGapLabel = (gap) => ({ none: '无', soft: '轻微', hard: '明显' }[gap] || gap)

    const queryReasonLabel = (reason) => {
      const labels = {
        multi_part_query: '包含多个子问题',
        comparison_query: '需要比较或权衡',
        cross_document_query: '明确要求跨文档',
        causal_query: '包含因果问题',
        analytical_query: '需要分析或诊断',
        long_query: '问题较长',
        ambiguous_reference: '存在缺少上下文的指代',
        no_evidence: '没有召回证据',
        retrieval_degraded: '检索器发生降级',
        single_retriever_evidence: '证据仅由单路检索支持',
        low_top_score: '最高证据分数偏低',
        cross_document_evidence_gap: '跨文档证据覆盖不足',
        weak_rank_separation: '多个来源排名接近',
        simple_query_high_confidence: '简单问题且证据置信度足够'
      }
      return labels[reason] || reason
    }

    const deepOutcomeLabel = (deep) => {
      if (!deep.activated) return '问题简单且证据充分，智能跳过额外模型调用'
      return ({ completed: '增强链路完成', partial_fallback: '部分增强失败，已安全回退' }[deep.outcome] || deep.outcome)
    }

    const enhancementOutcomeLabel = (outcome) => {
      const labels = {
        rewrite_not_required: '未触发',
        rewrite_completed: '完成',
        rewrite_model_error: '模型错误，已回退',
        rewrite_timeout: '超时，已回退',
        rewrite_invalid_output: '输出无效，已回退',
        rerank_not_required: '未触发',
        rerank_completed: '完成',
        rerank_model_error: '模型错误，已回退',
        rerank_timeout: '超时，已回退',
        rerank_invalid_output: '输出无效，已回退'
      }
      return labels[outcome] || '未触发'
    }

    const toggleKnowledgeSearch = () => {
      knowledgeSearchOpen.value = !knowledgeSearchOpen.value
      if (knowledgeSearchOpen.value && !knowledgeQuery.value.trim() && inputMessage.value.trim()) {
        knowledgeQuery.value = inputMessage.value.trim()
      }
    }

    const searchKnowledge = async () => {
      const query = knowledgeQuery.value.trim()
      if (!query || searchingKnowledge.value) return
      searchingKnowledge.value = true
      knowledgeAnswer.value = null
      knowledgeSearchResults.value = []
      knowledgeSearchDiagnostics.value = null
      try {
        const response = await api.post('/knowledge/search', { query, top_k: 5 })
        knowledgeSearchResults.value = response.data?.hits || []
        knowledgeSearchDiagnostics.value = response.data?.diagnostics || null
      } catch (error) {
        console.error('Knowledge search error:', error)
        ElMessage.error(error.response?.data?.message || '知识检索暂时不可用')
      } finally {
        searchingKnowledge.value = false
      }
    }

    const answerKnowledge = async (deep = false) => {
      const question = knowledgeQuery.value.trim()
      if (!question || answeringKnowledge.value) return
      answeringKnowledge.value = true
      answeringKnowledgeMode.value = deep ? 'deep' : 'fast'
      knowledgeAnswer.value = null
      try {
        const endpoint = deep ? '/knowledge/deep-answer' : '/knowledge/answer'
        const response = await api.post(endpoint, { question, top_k: 5 })
        knowledgeAnswer.value = response.data || null
        if (knowledgeAnswer.value?.result?.resolved) {
          ElMessage.success('回答已通过证据门和引用校验')
        } else {
          ElMessage.warning('证据不足，系统未调用模型生成结论')
        }
      } catch (error) {
        console.error('Knowledge answer error:', error)
        ElMessage.error(error.response?.data?.message || '知识库回答暂时不可用')
      } finally {
        answeringKnowledge.value = false
        answeringKnowledgeMode.value = ''
      }
    }

    const evidenceForCitation = (citation) => {
      const evidence = knowledgeAnswer.value?.result?.evidence || []
      return evidence.find(item => item.id === citation.evidence_id) || {}
    }

    const loadKnowledgeDocuments = async () => {
      try {
        const response = await api.get('/knowledge/documents')
        knowledgeDocuments.value = response.data?.documents || []
        if (!indexedKnowledgeDocuments.value.some(document => document.id === versionTargetDocumentId.value)) {
          versionTargetDocumentId.value = indexedKnowledgeDocuments.value[0]?.id || ''
        }
        const hasPendingDocument = knowledgeDocuments.value.some(document =>
          document.status === 'uploaded' || document.status === 'parsing'
        )
        if (hasPendingDocument || pendingVersionJob.value) {
          startKnowledgePolling()
        } else {
          stopKnowledgePolling()
        }
      } catch (error) {
        console.error('Load knowledge documents error:', error)
      }
    }

    const startKnowledgePolling = () => {
      if (!knowledgePollTimer) {
        knowledgePollTimer = window.setInterval(async () => {
          await pollPendingVersionJob()
          await loadKnowledgeDocuments()
        }, 3000)
      }
    }

    const stopKnowledgePolling = () => {
      if (knowledgePollTimer) {
        window.clearInterval(knowledgePollTimer)
        knowledgePollTimer = null
      }
    }

    const handleFileUpload = async (event) => {
      const file = event.target.files[0]
      if (!file) return

      const fileName = file.name.toLowerCase()
      const allowedExtensions = ['.md', '.txt', '.json', '.yaml', '.yml', '.go']
      if (!allowedExtensions.some(extension => fileName.endsWith(extension))) {
        ElMessage.error('支持 .md、.txt、.json、.yaml、.yml 和 .go 文件')
        // 清空文件输入
        if (fileInput.value) {
          fileInput.value.value = ''
        }
        return
      }

      try {
        uploading.value = true
        const formData = new FormData()
        formData.append('file', file)

        const response = await api.post('/knowledge/documents', formData, {
          headers: {
            'Content-Type': 'multipart/form-data'
          }
        })

        if (response.data?.document) {
          if (response.data.duplicate) {
            ElMessage.success('文档已存在，沿用原索引任务')
          } else {
            ElMessage.success('文档已接收，等待索引')
          }
          await loadKnowledgeDocuments()
        } else {
          ElMessage.error('上传失败')
        }
      } catch (error) {
        console.error('File upload error:', error)
        ElMessage.error(error.response?.data?.message || '文件上传失败')
      } finally {
        uploading.value = false
        // 清空文件输入
        if (fileInput.value) {
          fileInput.value.value = ''
        }
      }
    }

    const triggerVersionUpload = () => {
      if (!versionTargetDocumentId.value) {
        ElMessage.warning('请先选择要更新的活动文档')
        return
      }
      versionFileInput.value?.click()
    }

    const pollPendingVersionJob = async () => {
      const job = pendingVersionJob.value
      if (!job?.id) return
      try {
        const response = await api.get(`/knowledge/jobs/${job.id}`)
        const latest = response.data?.job
        if (!latest) return
        pendingVersionJob.value = latest
        if (latest.status === 'completed') {
          if (latest.job_type === 'document_delete') {
            ElMessage.success('文档已删除，Redis 索引清理完成')
          } else {
            ElMessage.success(`文档 v${latest.version} 索引完成，活动版本已原子切换`)
          }
          pendingVersionJob.value = null
        } else if (latest.status === 'failed') {
          if (latest.job_type === 'document_delete') {
            ElMessage.warning(`文档已从查询中移除，但 Redis 清理失败并已进入死信（${latest.last_error_code || 'UNKNOWN'}）`)
          } else {
            ElMessage.warning(`文档 v${latest.version} 索引失败，旧版本保持可用（${latest.last_error_code || 'UNKNOWN'}）`)
          }
          pendingVersionJob.value = null
        }
      } catch (error) {
        console.error('Poll knowledge version job error:', error)
      }
    }

    const handleVersionUpload = async (event) => {
      const file = event.target.files[0]
      if (!file || !versionTargetDocumentId.value) return
      const fileName = file.name.toLowerCase()
      const allowedExtensions = ['.md', '.txt', '.json', '.yaml', '.yml', '.go']
      if (!allowedExtensions.some(extension => fileName.endsWith(extension))) {
        ElMessage.error('支持 .md、.txt、.json、.yaml、.yml 和 .go 文件')
        if (versionFileInput.value) versionFileInput.value.value = ''
        return
      }
      try {
        uploadingVersion.value = true
        const formData = new FormData()
        formData.append('file', file)
        const response = await api.post(`/knowledge/documents/${versionTargetDocumentId.value}/versions`, formData, {
          headers: { 'Content-Type': 'multipart/form-data' }
        })
        pendingVersionJob.value = response.data?.job || null
        if (response.data?.duplicate) {
          ElMessage.info(`该内容已存在于 v${response.data?.pending_version || response.data?.job?.version}`)
        } else {
          ElMessage.success(`已接收 v${response.data?.pending_version}；v${response.data?.previous_version} 将持续生效直到新索引成功`)
        }
        startKnowledgePolling()
      } catch (error) {
        console.error('Version upload error:', error)
        ElMessage.error(error.response?.data?.message || '文档新版本上传失败')
      } finally {
        uploadingVersion.value = false
        if (versionFileInput.value) versionFileInput.value.value = ''
      }
    }

    const rebuildSelectedDocument = async () => {
      if (!versionTargetDocumentId.value || rebuildingDocument.value) return
      try {
        rebuildingDocument.value = true
        const response = await api.post(`/knowledge/documents/${versionTargetDocumentId.value}/rebuild`)
        pendingVersionJob.value = response.data?.job || null
        ElMessage.success(`已创建重建候选 v${response.data?.pending_version}；活动 v${response.data?.previous_version} 不受影响`)
        startKnowledgePolling()
      } catch (error) {
        console.error('Rebuild document error:', error)
        ElMessage.error(error.response?.data?.message || '文档重建任务创建失败')
      } finally {
        rebuildingDocument.value = false
      }
    }

    const deleteSelectedDocument = async () => {
      const document = indexedKnowledgeDocuments.value.find(item => item.id === versionTargetDocumentId.value)
      if (!document || deletingDocument.value) return
      if (!window.confirm(`确定删除文档“${document.display_name}”吗？删除后会立即停止参与回答。`)) return
      try {
        deletingDocument.value = true
        const response = await api.delete(`/knowledge/documents/${document.id}`)
        pendingVersionJob.value = response.data?.job || null
        ElMessage.success('文档已立即退出知识库，后台正在清理 Redis 索引')
        await loadKnowledgeDocuments()
        startKnowledgePolling()
      } catch (error) {
        console.error('Delete document error:', error)
        ElMessage.error(error.response?.data?.message || '文档删除失败')
      } finally {
        deletingDocument.value = false
      }
    }

    onMounted(() => {
      loadSessions()
      loadKnowledgeDocuments()
      if (sessionStorage.getItem(diagnosticModeStorageKey) === '1' && sessionStorage.getItem(diagnosticRunStorageKey)) {
        diagnosticMode.value = true
        restoreDiagnosticRun()
      }
    })

    onUnmounted(() => {
      stopKnowledgePolling()
    })

    // expose to template
    return {
      sessions: computed(() => Object.values(sessions.value)),
      currentSessionId,
      tempSession,
      currentMessages,
      inputMessage,
      loading,
      messagesRef,
      messageInput,
      isStreaming,
      knowledgeRequired,
      uploading,
      fileInput,
      versionFileInput,
      uploadingVersion,
      versionTargetDocumentId,
      pendingVersionJob,
      rebuildingDocument,
      deletingDocument,
      indexedKnowledgeDocuments,
      knowledgeDocuments,
      knowledgeSearchOpen,
      knowledgeQuery,
      searchingKnowledge,
      knowledgeSearchResults,
      knowledgeSearchDiagnostics,
      answeringKnowledge,
      answeringKnowledgeMode,
      knowledgeAnswer,
      diagnosticMode,
      activeDiagnosticRun,
      diagnosticRecovered,
      restoringDiagnosticRun,
      diagnosticEvaluationOpen,
      loadingDiagnosticEvaluation,
      diagnosticEvaluation,
      contextCompressionOpen,
      loadingContextCompression,
      contextCompression,
      contextEvaluation,
      memoryPreviewOpen,
      loadingMemoryPreview,
      memoryPreview,
      memoryEvaluationOpen,
      loadingMemoryEvaluation,
      memoryEvaluation,
      toolRuntimeOpen,
      loadingToolCatalog,
      toolCatalog,
      invokingTool,
      toolResult,
      profileMemories,
      profileDrafts,
      profileMemoryBusy,
      resolutionProposal,
      resolutionText,
      resolutionAcknowledged,
      loadingResolution,
      confirmedResolution,
      documentStatusLabel,
      jobStatusLabel,
      retrievalModeLabel,
      queryComplexityLabel,
      queryGapLabel,
      queryReasonLabel,
      deepOutcomeLabel,
      enhancementOutcomeLabel,
      intentLabel,
      intentStageLabel,
      diagnosticStateLabel,
      isDiagnosticTerminal,
      incidentIndexLabel,
      metricPercent,
      memoryCacheLabel,
      memoryContextKindLabel,
      profileKeyLabel,
      profileStatusLabel,
      profileSourceLabel,
      toggleMemoryPreview,
      loadMemoryPreview,
      rebuildWorkingMemory,
      correctProfileMemory,
      deleteProfileMemory,
      toggleMemoryEvaluation,
      toggleToolRuntime,
      invokeGovernedTool,
      formatToolData,
      toggleDiagnosticEvaluation,
      toggleContextCompression,
      formattedFacts,
      previewResolution,
      closeResolutionProposal,
      confirmResolution,
      loadConfirmedResolution,
      renderMarkdown,
      playTTS,
      createNewSession,
      switchSession,
      syncHistory,
      sendMessage,
      triggerFileUpload,
      handleFileUpload,
      triggerVersionUpload,
      handleVersionUpload,
      rebuildSelectedDocument,
      deleteSelectedDocument,
      toggleKnowledgeSearch,
      searchKnowledge,
      answerKnowledge,
      cancelDiagnosticRun,
      resetDiagnosticRun,
      onDiagnosticModeChanged,
      evidenceForCitation
    }
  }
}
</script>

<style scoped>
.ai-chat-container {
  height: 100vh;
  display: flex;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  position: relative;
  overflow: hidden;
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial;
  color: #222;
}

.ai-chat-container::before {
  content: '';
  position: absolute;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: url('data:image/svg+xml,<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><circle cx="20" cy="20" r="2" fill="rgba(255,255,255,0.08)"/><circle cx="80" cy="80" r="2" fill="rgba(255,255,255,0.08)"/><circle cx="40" cy="60" r="1" fill="rgba(255,255,255,0.06)"/><circle cx="60" cy="30" r="1.5" fill="rgba(255,255,255,0.06)"/></svg>');
  animation: float 20s ease-in-out infinite;
  opacity: 0.25;
}

@keyframes float {
  0%, 100% { transform: translateY(0px) rotate(0deg); }
  50% { transform: translateY(-20px) rotate(180deg); }
}

.session-list {
  width: 280px;
  height: 100vh;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(15px);
  border-right: 1px solid rgba(0, 0, 0, 0.08);
  box-shadow: 2px 0 20px rgba(0, 0, 0, 0.08);
  position: relative;
  z-index: 2;
}

.session-list-header {
  padding: 20px;
  text-align: center;
  font-weight: 600;
  background: linear-gradient(135deg, rgba(102, 126, 234, 0.06) 0%, rgba(103, 194, 58, 0.06) 100%);
  border-bottom: 1px solid rgba(0, 0, 0, 0.06);
  display: flex;
  flex-direction: column;
  gap: 12px;
  align-items: center;
}

.new-chat-btn {
  width: 100%;
  padding: 12px 0;
  cursor: pointer;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  border: none;
  border-radius: 12px;
  font-size: 14px;
  font-weight: 600;
  box-shadow: 0 4px 15px rgba(102, 126, 234, 0.28);
  transition: all 0.25s ease;
  position: relative;
  overflow: hidden;
}

.new-chat-btn::before {
  content: '';
  position: absolute;
  top: 0;
  left: -100%;
  width: 100%;
  height: 100%;
  background: linear-gradient(90deg, transparent, rgba(255,255,255,0.12), transparent);
  transition: left 0.5s;
}

.new-chat-btn:hover::before {
  left: 100%;
}

.new-chat-btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 8px 25px rgba(102, 126, 234, 0.36);
}

.session-list-ul {
  list-style: none;
  padding: 0;
  margin: 0;
  flex: 1;
  overflow-y: auto;
}

.session-item {
  padding: 15px 20px;
  cursor: pointer;
  border-bottom: 1px solid rgba(0, 0, 0, 0.03);
  transition: all 0.2s ease;
  position: relative;
  color: #2c3e50;
}

.session-item.active {
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  font-weight: 600;
  box-shadow: inset 0 0 20px rgba(102, 126, 234, 0.2);
}

.session-item:hover {
  background: rgba(102, 126, 234, 0.06);
  transform: translateX(4px);
}

/* chat section */
.chat-section {
  flex: 1;
  display: flex;
  flex-direction: column;
  position: relative;
  z-index: 1;
  min-width: 0;
  min-height: 0;
  overflow: hidden;
}

.top-bar {
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(10px);
  color: #2c3e50;
  display: flex;
  align-items: center;
  padding: 12px 24px;
  box-shadow: 0 2px 14px rgba(0, 0, 0, 0.06);
  border-bottom: 1px solid rgba(0, 0, 0, 0.06);
  gap: 12px;
  flex-wrap: wrap;
}

.memory-workbench {
  margin: 12px 20px 0;
  padding: 16px;
  border-radius: 14px;
  border: 1px solid #b8d9c7;
  background: linear-gradient(135deg, #f2fff7 0%, #eef8ff 100%);
  color: #243746;
}

.memory-workbench-header,
.memory-workbench-header > div,
.memory-stats {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}

.memory-workbench-header {
  justify-content: space-between;
}

.memory-workbench-header > div:first-child {
  align-items: flex-start;
  flex-direction: column;
}

.memory-workbench-header span,
.memory-stats,
.memory-context-items span {
  color: #547080;
  font-size: 13px;
}

.memory-workbench button,
.memory-toggle-btn {
  border: 0;
  border-radius: 8px;
  padding: 8px 12px;
  cursor: pointer;
  background: #3b9f74;
  color: #fff;
}

.memory-workbench button:disabled,
.memory-toggle-btn:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.memory-loading,
.memory-stats,
.memory-boundary,
.memory-workbench details {
  margin-top: 12px;
}

.memory-cache-badge {
  border-radius: 999px;
  padding: 4px 9px;
  background: #d7f5e5;
  color: #176744;
  font-weight: 700;
}

.cache-mysql_fallback_cache_unavailable {
  background: #fff1d6;
  color: #8a5a00;
}

.memory-boundary {
  border-left: 4px solid #5aa6b8;
  padding: 8px 12px;
  background: rgba(255, 255, 255, 0.72);
  font-size: 13px;
}

.memory-context-items {
  max-height: 280px;
  overflow: auto;
  padding-left: 24px;
}

.memory-context-items li {
  margin: 9px 0;
}

.memory-context-items span {
  margin-left: 8px;
}

.memory-context-items p {
  margin: 4px 0 0;
  white-space: pre-wrap;
  word-break: break-word;
}

.memory-no-session {
  margin-top: 12px;
  padding: 10px;
  border-radius: 8px;
  color: #607380;
  background: rgba(255, 255, 255, 0.7);
  font-size: 13px;
}

.profile-memory-panel {
  margin-top: 14px;
  padding-top: 13px;
  border-top: 1px solid #b8d9c7;
}

.profile-memory-summary,
.profile-memory-card-header,
.profile-memory-actions,
.profile-memory-actions > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.profile-memory-summary span,
.profile-memory-panel > p,
.profile-memory-actions small {
  color: #607380;
  font-size: 12px;
}

.profile-memory-panel > p {
  margin: 7px 0 10px;
}

.profile-memory-list {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
  gap: 9px;
}

.profile-memory-list article {
  padding: 10px;
  border: 1px solid #b9d7c9;
  border-radius: 9px;
  background: #fff;
}

.profile-memory-list article.profile-candidate {
  border-color: #d8bd70;
  background: #fffdf5;
}

.profile-memory-list article.profile-conflicted {
  border-color: #e39a9a;
  background: #fff7f7;
}

.profile-memory-card-header span {
  color: #667783;
  font-size: 12px;
}

.profile-memory-list input {
  width: 100%;
  box-sizing: border-box;
  margin: 8px 0;
  padding: 8px 9px;
  border: 1px solid #bfd2ca;
  border-radius: 7px;
}

.profile-memory-actions button {
  padding: 6px 9px;
}

.profile-memory-actions .profile-delete-btn {
  color: #fff;
  background: #d45d5d;
}

.diagnostic-workbench {
  max-height: 48vh;
  overflow-y: auto;
  padding: 14px 20px;
  color: #23344d;
  background: rgba(247, 250, 255, 0.98);
  border-bottom: 2px solid rgba(103, 126, 234, 0.22);
  box-shadow: 0 5px 18px rgba(31, 45, 85, 0.08);
}

.diagnostic-workbench-header,
.diagnostic-run-summary,
.hypothesis-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  flex-wrap: wrap;
  gap: 8px 14px;
}

.diagnostic-workbench-header > div:first-child {
  display: grid;
  gap: 3px;
}

.diagnostic-workbench-header span,
.diagnostic-run-summary,
.diagnostic-steps li span {
  color: #65758b;
  font-size: 12px;
}

.diagnostic-workbench-header .diagnostic-recovered {
  display: inline-flex;
  width: fit-content;
  margin-top: 6px;
  padding: 3px 8px;
  border-radius: 999px;
  color: #176b45;
  background: #dff7e9;
  font-weight: 600;
}

.diagnostic-actions {
  display: flex;
  gap: 8px;
}

.diagnostic-actions button {
  padding: 6px 11px;
  border: 1px solid rgba(90, 103, 216, 0.3);
  border-radius: 8px;
  color: #4b55a5;
  background: #fff;
  cursor: pointer;
}

.diagnostic-actions button:disabled {
  opacity: 0.5;
  cursor: not-allowed;
}

.diagnostic-evaluation {
  margin: 12px 0;
  padding: 14px;
  border: 1px solid #c9d8ff;
  border-radius: 12px;
  background: #f7f9ff;
}

.diagnostic-evaluation-loading {
  color: #52627d;
}

.diagnostic-evaluation-title,
.diagnostic-evaluation-title > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
}

.diagnostic-evaluation-title > div {
  align-items: baseline;
  justify-content: flex-start;
  flex-wrap: wrap;
}

.diagnostic-evaluation-title span,
.diagnostic-evaluation details small {
  color: #64728b;
  font-size: 12px;
}

.evaluation-gate {
  padding: 4px 9px;
  border-radius: 999px;
  font-weight: 700;
}

.evaluation-gate.passed {
  color: #176b45;
  background: #dcf7e8;
}

.evaluation-gate.failed {
  color: #a23131;
  background: #ffe4e4;
}

.diagnostic-evaluation-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 8px;
  margin: 12px 0;
}

.diagnostic-evaluation-grid > div {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 10px;
  border-radius: 9px;
  background: #ffffff;
  border: 1px solid #e4e9f5;
}

.diagnostic-evaluation-grid strong {
  color: #3657ba;
  font-size: 18px;
}

.diagnostic-evaluation-grid span,
.evaluation-candidate-warning span {
  color: #596781;
  font-size: 12px;
}

.evaluation-candidate-warning {
  display: flex;
  flex-direction: column;
  gap: 4px;
  margin: 10px 0;
  padding: 10px;
  border-radius: 9px;
  color: #7a5715;
  background: #fff4d6;
  border: 1px solid #f0d78f;
}

.evaluation-category-list {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 8px 0;
}

.evaluation-category-list span {
  padding: 3px 7px;
  border-radius: 999px;
  color: #485977;
  background: #e8eefc;
  font-size: 11px;
}

@media (max-width: 760px) {
  .diagnostic-evaluation-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

.diagnostic-empty,
.diagnostic-waiting {
  margin-top: 10px;
  padding: 10px 12px;
  border-radius: 9px;
  background: rgba(103, 126, 234, 0.08);
}

.diagnostic-waiting {
  color: #7a5312;
  background: #fff7e6;
  border: 1px solid #ffd591;
}

.diagnostic-run-summary {
  justify-content: flex-start;
  margin-top: 12px;
}

.run-state-badge {
  padding: 5px 9px;
  border-radius: 999px;
  color: #3451a3;
  background: #e9efff;
  font-weight: 700;
}

.state-waiting_user {
  color: #9a6200;
  background: #fff1cc;
}

.state-succeeded {
  color: #176e4b;
  background: #ddf6e9;
}

.state-cancelled,
.state-budget_exceeded,
.state-failed {
  color: #9c3434;
  background: #ffe7e7;
}

.diagnostic-steps {
  margin-top: 12px;
  padding: 9px 12px;
  border: 1px solid rgba(103, 126, 234, 0.18);
  border-radius: 9px;
  background: #fff;
}

.diagnostic-steps summary {
  cursor: pointer;
  font-weight: 700;
}

.diagnostic-steps ol,
.diagnostic-hypotheses ol {
  margin: 8px 0 0;
  padding-left: 22px;
}

.diagnostic-steps li {
  display: grid;
  gap: 2px;
  margin: 5px 0;
}

.case-memory-recall {
  margin-top: 12px;
  padding: 12px;
  border: 1px solid #a7c8bd;
  border-radius: 10px;
  background: #f2fbf7;
}

.case-memory-recall.case-unavailable {
  border-color: #ddc98e;
  background: #fffaf0;
}

.case-memory-header,
.case-memory-list article > div {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.case-memory-header span,
.case-memory-list span,
.case-memory-list small {
  color: #617584;
  font-size: 12px;
}

.case-memory-disclaimer {
  margin: 8px 0;
  padding-left: 9px;
  border-left: 3px solid #4a9c7d;
  color: #465b68;
  font-size: 13px;
}

.case-memory-list {
  display: grid;
  gap: 8px;
}

.case-memory-list article {
  padding: 9px 10px;
  border-radius: 8px;
  background: #fff;
}

.case-memory-list details {
  margin-top: 7px;
}

.case-memory-list summary {
  cursor: pointer;
  color: #356b5a;
  font-weight: 700;
}

.case-memory-list p {
  margin: 6px 0;
  color: #4a5e6d;
  font-size: 13px;
}

.diagnostic-hypotheses {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(300px, 1fr));
  gap: 10px;
  margin-top: 10px;
}

.diagnostic-hypotheses article {
  padding: 12px;
  border: 1px solid rgba(103, 126, 234, 0.2);
  border-radius: 10px;
  background: #fff;
}

.diagnostic-hypotheses p {
  margin: 8px 0;
  color: #526176;
  font-size: 13px;
}

.diagnostic-hypotheses li {
  margin-bottom: 7px;
}

.diagnostic-hypotheses small {
  color: #6d7b90;
}

.resolution-preview-btn {
  width: 100%;
  margin-top: 8px;
  padding: 8px 10px;
  border: 1px solid #98a9e8;
  border-radius: 8px;
  color: #4054a5;
  background: #eef2ff;
  cursor: pointer;
}

.resolution-preview-btn:disabled,
.resolution-confirm-btn:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.resolution-proposal,
.confirmed-resolution {
  margin-top: 12px;
  padding: 14px;
  border-radius: 12px;
}

.resolution-proposal {
  border: 1px solid #e0bd67;
  background: #fffbeb;
}

.resolution-proposal-header,
.confirmed-resolution-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.confirmed-resolution-actions {
  display: flex;
  align-items: center;
  gap: 7px;
}

.confirmed-resolution-actions button {
  padding: 4px 7px;
  border: 1px solid #9bd4b6;
  border-radius: 7px;
  color: #246346;
  background: #fff;
  cursor: pointer;
}

.resolution-proposal-header > div {
  display: grid;
  gap: 3px;
}

.resolution-proposal-header span,
.confirmed-resolution small {
  color: #68768b;
  font-size: 12px;
}

.resolution-proposal-header button {
  border: 0;
  color: #6f5a25;
  background: transparent;
  cursor: pointer;
}

.resolution-proposal-content p,
.confirmed-resolution p {
  margin: 8px 0;
  color: #435169;
  font-size: 13px;
}

.resolution-input-label {
  display: grid;
  gap: 6px;
  margin-top: 12px;
  color: #4b5363;
  font-size: 13px;
}

.resolution-input-label textarea {
  width: 100%;
  box-sizing: border-box;
  padding: 9px 10px;
  border: 1px solid #d7be7e;
  border-radius: 8px;
  resize: vertical;
  font: inherit;
}

.resolution-confirm-check {
  display: flex;
  align-items: flex-start;
  gap: 7px;
  margin: 10px 0;
  color: #705821;
  font-size: 12px;
}

.resolution-confirm-btn {
  padding: 9px 13px;
  border: 0;
  border-radius: 8px;
  color: white;
  background: #5267c9;
  cursor: pointer;
}

.confirmed-resolution {
  border: 1px solid #9bd4b6;
  background: #effbf4;
}

.incident-index-badge {
  padding: 4px 8px;
  border-radius: 999px;
  color: #7e5d16;
  background: #fff1c7;
  font-size: 12px;
  font-weight: 700;
}

.incident-index-badge.index-indexed {
  color: #176b45;
  background: #d9f4e5;
}

.incident-index-badge.index-failed {
  color: #9c3434;
  background: #ffe3e3;
}

.knowledge-status {
  display: flex;
  justify-content: space-between;
  align-items: center;
  flex-wrap: wrap;
  gap: 16px;
  padding: 9px 24px;
  color: #36506c;
  background: rgba(239, 247, 255, 0.96);
  border-bottom: 1px solid rgba(64, 158, 255, 0.18);
  font-size: 13px;
}

.knowledge-document-details {
  flex-basis: 100%;
  padding-top: 2px;
  border-top: 1px dashed rgba(64, 158, 255, 0.2);
}

.knowledge-document-details summary {
  width: fit-content;
  cursor: pointer;
  color: #2f6ea7;
  font-weight: 600;
}

.knowledge-document-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
  gap: 8px;
  margin-top: 8px;
}

.knowledge-document-card {
  padding: 9px 11px;
  border: 1px solid rgba(64, 158, 255, 0.18);
  border-radius: 8px;
  background: rgba(255, 255, 255, 0.82);
}

.knowledge-document-card > div:first-child {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.document-status-badge {
  flex-shrink: 0;
  padding: 2px 7px;
  border-radius: 999px;
  background: #e8eef5;
  color: #52677f;
  font-size: 11px;
}

.document-status-badge.status-indexed {
  background: #e2f7ee;
  color: #137a52;
}

.document-status-badge.status-failed {
  background: #fff0f0;
  color: #c23d4b;
}

.document-status-badge.status-uploaded,
.document-status-badge.status-parsing {
  background: #fff4de;
  color: #a56500;
}

.document-version-line {
  margin-top: 5px;
  color: #708399;
  font-size: 11px;
}

.document-error-code {
  margin-top: 5px;
  color: #b23a48;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 11px;
}

.knowledge-version-controls {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 420px;
}

.knowledge-version-controls select {
  max-width: 260px;
  padding: 6px 8px;
  border: 1px solid #b9cce3;
  border-radius: 7px;
  background: #fff;
  color: #36506c;
}

.knowledge-version-controls button {
  padding: 6px 10px;
  border: 0;
  border-radius: 7px;
  background: #409eff;
  color: #fff;
  cursor: pointer;
}

.knowledge-version-controls button:disabled {
  opacity: 0.55;
  cursor: not-allowed;
}

.knowledge-version-controls .delete-document-btn {
  background: #f56c6c;
}

.query-assessment {
  margin: 8px 0 12px;
  padding: 10px 12px;
  border-left: 3px solid #409eff;
  border-radius: 4px;
  background: #f2f8ff;
  color: #3f4a5a;
  font-size: 13px;
}

.query-assessment-reasons {
  margin-top: 4px;
  color: #6b7280;
}

.query-assessment-action {
  margin-top: 5px;
  color: #355f93;
}

.version-pending {
  color: #b36b00;
  white-space: nowrap;
}

.knowledge-latest {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.knowledge-search-panel {
  max-height: 46vh;
  overflow-y: auto;
  padding: 14px 24px 18px;
  background: rgba(248, 251, 255, 0.98);
  border-bottom: 1px solid rgba(64, 158, 255, 0.2);
  box-shadow: 0 8px 18px rgba(27, 54, 93, 0.08);
}

.knowledge-search-form {
  display: flex;
  gap: 10px;
}

.knowledge-search-form input {
  flex: 1;
  min-width: 0;
  padding: 10px 13px;
  border: 1px solid #c9d7e8;
  border-radius: 9px;
  outline: none;
}

.knowledge-search-form input:focus {
  border-color: #409eff;
  box-shadow: 0 0 0 3px rgba(64, 158, 255, 0.12);
}

.knowledge-search-form button,
.search-toggle-btn {
  padding: 8px 14px;
  border: none;
  border-radius: 9px;
  color: white;
  background: linear-gradient(135deg, #409eff 0%, #536dfe 100%);
  cursor: pointer;
  font-weight: 600;
}

.knowledge-search-form .answer-evidence-btn {
  background: linear-gradient(135deg, #19a974 0%, #2f80ed 100%);
}

.knowledge-search-form .deep-answer-btn {
  background: linear-gradient(135deg, #7c3aed 0%, #2563eb 100%);
}

.knowledge-search-form button:disabled {
  background: #b8c2cf;
  cursor: not-allowed;
}

.knowledge-search-summary {
  margin: 11px 0;
  color: #52677f;
  font-size: 12px;
}

.knowledge-answer {
  margin-top: 12px;
  padding: 13px;
  border: 1px solid rgba(25, 169, 116, 0.35);
  border-radius: 10px;
  background: rgba(238, 252, 247, 0.96);
}

.knowledge-answer.insufficient {
  border-color: rgba(230, 162, 60, 0.45);
  background: rgba(255, 248, 235, 0.96);
}

.knowledge-answer-header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  color: #31506b;
  font-size: 13px;
}

.knowledge-answer-header span {
  color: #6e8195;
  font-size: 11px;
}

.knowledge-answer-text {
  margin-top: 10px;
  white-space: pre-wrap;
  line-height: 1.65;
  color: #273b4d;
}

.deep-diagnostics {
  margin-top: 10px;
  padding: 9px 10px;
  border-radius: 7px;
  background: rgba(124, 58, 237, 0.08);
  color: #4c3d72;
  font-size: 12px;
  line-height: 1.55;
}

.deep-diagnostics details {
  margin-top: 5px;
}

.deep-budget-observation,
.deep-fallback-reasons {
  margin-top: 5px;
}

.deep-fallback-reasons {
  color: #9a630d;
}

.knowledge-follow-up {
  margin-top: 9px;
  color: #9a630d;
  font-size: 13px;
}

.knowledge-citations {
  margin-top: 10px;
}

.knowledge-citations details {
  margin-top: 6px;
  padding: 7px 9px;
  border-radius: 7px;
  background: rgba(255, 255, 255, 0.8);
  color: #536a82;
  font-size: 12px;
}

.knowledge-citations summary {
  cursor: pointer;
}

.knowledge-citations pre {
  max-height: 140px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
}

.knowledge-search-results {
  display: grid;
  gap: 10px;
}

.evidence-card {
  padding: 11px 13px;
  border: 1px solid #dce7f5;
  border-radius: 10px;
  background: white;
}

.evidence-title {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  color: #2d425c;
  font-size: 13px;
}

.evidence-title span,
.evidence-location {
  color: #788da6;
  font-size: 11px;
}

.evidence-location {
  margin-top: 5px;
}

.evidence-card pre {
  margin: 9px 0 0;
  padding: 9px;
  max-height: 130px;
  overflow: auto;
  white-space: pre-wrap;
  word-break: break-word;
  border-radius: 7px;
  background: #f6f8fb;
  color: #34495e;
  font-family: Consolas, monospace;
  font-size: 12px;
}

.knowledge-search-empty {
  padding: 16px 0 3px;
  color: #8a98a8;
  font-size: 13px;
}

.back-btn {
  background: rgba(255, 255, 255, 0.22);
  border: 1px solid rgba(0, 0, 0, 0.06);
  color: #2c3e50;
  padding: 8px 14px;
  border-radius: 10px;
  cursor: pointer;
  font-weight: 600;
  transition: all 0.2s ease;
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
}

.back-btn:hover {
  background: rgba(255, 255, 255, 0.32);
  transform: translateY(-2px);
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.08);
}

.sync-btn {
  background: linear-gradient(135deg, #67c23a 0%, #409eff 100%);
  color: white;
  padding: 8px 14px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
  box-shadow: 0 4px 12px rgba(103, 194, 58, 0.2);
  transition: all 0.2s ease;
}

.sync-btn:disabled {
  background: #ccc;
  box-shadow: none;
  cursor: not-allowed;
}

.route-mode {
  margin-left: 6px;
  padding: 7px 12px;
  border-radius: 999px;
  background: rgba(64, 158, 255, 0.1);
  color: #337ecc;
  font-size: 13px;
  font-weight: 700;
}

.knowledge-mode {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 6px 9px;
  border-radius: 999px;
  background: rgba(25, 169, 116, 0.1);
  color: #167a57;
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}

.diagnostic-mode {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 6px 9px;
  border-radius: 999px;
  background: rgba(93, 78, 190, 0.11);
  color: #5946aa;
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}

.tool-runtime-toggle {
  padding: 8px 12px;
  border: none;
  border-radius: 10px;
  background: linear-gradient(135deg, #405a7d 0%, #526da8 100%);
  color: #fff;
  font-size: 12px;
  font-weight: 700;
  cursor: pointer;
  white-space: nowrap;
}

.tool-runtime-toggle:disabled {
  cursor: wait;
  opacity: 0.65;
}

.tool-runtime-panel {
  margin: 12px 20px 0;
  padding: 15px;
  border: 1px solid rgba(64, 90, 125, 0.18);
  border-radius: 14px;
  background: linear-gradient(145deg, rgba(246, 249, 255, 0.98), rgba(238, 244, 252, 0.98));
  box-shadow: 0 8px 24px rgba(45, 67, 99, 0.08);
}

.tool-runtime-header,
.tool-runtime-title,
.tool-result-heading {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.tool-runtime-header > div {
  display: grid;
  gap: 3px;
}

.tool-runtime-header span,
.tool-runtime-card p,
.tool-result,
.tool-runtime-empty {
  color: #60718a;
  font-size: 12px;
}

.tool-runtime-schema,
.tool-runtime-title span {
  padding: 4px 8px;
  border-radius: 999px;
  background: rgba(64, 90, 125, 0.1);
  color: #405a7d;
  font-weight: 700;
}

.tool-runtime-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(290px, 1fr));
  gap: 10px;
  margin-top: 12px;
}

.tool-runtime-card,
.tool-result {
  padding: 12px;
  border: 1px solid rgba(64, 90, 125, 0.14);
  border-radius: 11px;
  background: #fff;
}

.tool-runtime-card p {
  margin: 8px 0;
  line-height: 1.55;
}

.tool-runtime-card button {
  margin-top: 9px;
  padding: 7px 11px;
  border: none;
  border-radius: 8px;
  background: #405a7d;
  color: #fff;
  cursor: pointer;
  font-weight: 700;
}

.tool-health-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 7px;
}

.tool-runtime-tags {
  display: flex;
  flex-wrap: wrap;
  gap: 5px;
}

.tool-runtime-tags span {
  padding: 3px 6px;
  border-radius: 6px;
  background: #eef3fa;
  color: #526680;
  font-size: 11px;
}

.tool-result {
  display: grid;
  gap: 6px;
  margin-top: 11px;
}

.tool-result.success { border-left: 4px solid #34a879; }
.tool-result.failed { border-left: 4px solid #d66b6b; }

.tool-result pre {
  max-height: 220px;
  margin: 4px 0;
  padding: 10px;
  overflow: auto;
  border-radius: 8px;
  background: #172233;
  color: #d8e8fb;
  font-size: 11px;
  white-space: pre-wrap;
}

.routing-meta {
  margin-top: 9px;
  color: #8492a6;
  font-size: 11px;
  letter-spacing: 0.01em;
}

.shadow-intent-meta {
  margin-top: 4px;
  color: #7a5daf;
  font-weight: 650;
}

.chat-citations {
  display: grid;
  gap: 4px;
  margin-top: 8px;
  padding-top: 7px;
  border-top: 1px dashed rgba(64, 158, 255, 0.25);
  color: #607d9b;
  font-size: 11px;
}

.upload-btn {
  background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%);
  color: white;
  padding: 8px 14px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
  box-shadow: 0 4px 12px rgba(245, 87, 108, 0.2);
  transition: all 0.2s ease;
}

.upload-btn:hover:not(:disabled) {
  transform: translateY(-2px);
  box-shadow: 0 6px 16px rgba(245, 87, 108, 0.3);
}

.upload-btn:disabled {
  background: #ccc;
  box-shadow: none;
  cursor: not-allowed;
}

.chat-messages {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 30px;
  display: flex;
  flex-direction: column;
  gap: 18px;
  position: relative;
  z-index: 1;
}

/* scrollbar */
.chat-messages::-webkit-scrollbar {
  width: 8px;
}
.chat-messages::-webkit-scrollbar-thumb {
  background: rgba(0,0,0,0.12);
  border-radius: 8px;
}
.chat-messages::-webkit-scrollbar-track {
  background: transparent;
}

.message {
  max-width: 70%;
  padding: 14px 18px;
  border-radius: 18px;
  line-height: 1.6;
  word-wrap: break-word;
  position: relative;
  animation: messageSlideIn 0.28s ease-out;
  font-size: 15px;
  box-sizing: border-box;
}

@keyframes messageSlideIn {
  from {
    opacity: 0;
    transform: translateY(12px) scale(0.98);
  }
  to {
    opacity: 1;
    transform: translateY(0) scale(1);
  }
}

.user-message {
  align-self: flex-end;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  box-shadow: 0 6px 20px rgba(102, 126, 234, 0.16);
}

.user-message::after {
  content: '';
  position: absolute;
  bottom: -6px;
  right: 18px;
  width: 0;
  height: 0;
  border-left: 8px solid transparent;
  border-right: 8px solid transparent;
  border-top: 8px solid #764ba2;
}

.ai-message {
  align-self: flex-start;
  background: rgba(255, 255, 255, 0.95);
  backdrop-filter: blur(4px);
  color: #2c3e50;
  box-shadow: 0 6px 20px rgba(0, 0, 0, 0.06);
  border: 1px solid rgba(255, 255, 255, 0.3);
}

.ai-message::after {
  content: '';
  position: absolute;
  bottom: -6px;
  left: 18px;
  width: 0;
  height: 0;
  border-left: 8px solid transparent;
  border-right: 8px solid transparent;
  border-top: 8px solid rgba(255, 255, 255, 0.95);
}

.message-header {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 8px;
}

.message-header b {
  font-weight: 600;
}

.tts-btn {
  padding: 6px 10px;
  border-radius: 8px;
  font-size: 12px;
  cursor: pointer;
  background: linear-gradient(135deg, #67c23a 0%, #409eff 100%);
  color: white;
  border: none;
  transition: all 0.18s ease;
  box-shadow: 0 2px 8px rgba(103, 194, 58, 0.18);
}

.tts-btn:hover {
  transform: scale(1.05);
  box-shadow: 0 4px 12px rgba(103, 194, 58, 0.25);
}

.streaming-indicator {
  color: #999;
  font-weight: 600;
  margin-left: 6px;
}

/* message content */
.message-content {
  white-space: pre-wrap;
  word-break: break-word;
}

/* input area */
.chat-input {
  padding: 24px;
  background: rgba(255, 255, 255, 0.96);
  backdrop-filter: blur(8px);
  border-top: 1px solid rgba(0, 0, 0, 0.06);
  position: relative;
  z-index: 1;
  display: flex;
  align-items: flex-end;
  gap: 12px;
}

/* textarea 样式已移至 .input-wrapper textarea */

.send-btn {
  padding: 12px 22px;
  border: none;
  border-radius: 50px;
  background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
  color: white;
  font-size: 15px;
  font-weight: 600;
  cursor: pointer;
  box-shadow: 0 6px 20px rgba(102,126,234,0.18);
  transition: all 0.18s ease;
  flex-shrink: 0;
}

.send-btn:hover:not(:disabled) {
  transform: translateY(-3px) scale(1.02);
}

.send-btn:disabled {
  background: #ccc;
  box-shadow: none;
  cursor: not-allowed;
}

/* 输入框包裹器 */
.input-wrapper {
  position: relative;
  flex: 1;
}

.input-wrapper textarea {
  width: 100%;
  resize: none;
  border: 2px solid rgba(0, 0, 0, 0.06);
  border-radius: 12px;
  padding: 14px 16px;
  font-size: 15px;
  outline: none;
  background: rgba(255,255,255,0.96);
  color: #2c3e50;
  transition: all 0.18s ease;
  min-height: 20px;
  max-height: 160px;
  box-shadow: 0 2px 10px rgba(0,0,0,0.04);
  box-sizing: border-box;
}

.input-wrapper textarea:focus {
  border-color: #409eff;
  box-shadow: 0 8px 30px rgba(64,158,255,0.06);
  transform: translateY(-1px);
}

</style>
