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
        <span class="route-mode" title="系统会根据问题自动选择意图、策略与 Agent">✨ 智能路由</span>
        <label for="streamingMode" style="margin-left: 20px;">
          <input type="checkbox" id="streamingMode" v-model="isStreaming" />
          流式响应
        </label>
        <label for="knowledgeMode" class="knowledge-mode" title="显式要求统一聊天入口使用 rag_fast；关闭时普通聊天保持原路径">
          <input type="checkbox" id="knowledgeMode" v-model="knowledgeRequired" />
          知识库回答
        </label>
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
      <span v-if="pendingVersionJob" class="version-pending">
      v{{ pendingVersionJob.version }} {{ jobStatusLabel(pendingVersionJob.status) }}；旧版本继续生效
      </span>
      <input
      ref="versionFileInput"
      type="file"
      accept=".md,.txt,.json,.yaml,.yml,.go,text/markdown,text/plain,application/json,application/yaml"
      style="display: none"
      @change="handleVersionUpload"
      />
    </div>
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
            @click="answerKnowledge"
          >
            {{ answeringKnowledge ? '证据校验与回答中...' : '基于证据回答' }}
          </button>
        </div>
        <section v-if="knowledgeAnswer" :class="['knowledge-answer', { insufficient: !knowledgeAnswer.result.resolved }]">
          <div class="knowledge-answer-header">
            <strong>{{ knowledgeAnswer.result.resolved ? '✅ 证据门通过' : '⚠️ 证据不足' }}</strong>
            <span>{{ knowledgeAnswer.agent }} · {{ knowledgeAnswer.strategy }} · {{ knowledgeAnswer.evidence_gate.reason_code }}</span>
          </div>
          <div class="knowledge-answer-text">{{ knowledgeAnswer.result.answer }}</div>
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
            自动路由 · {{ message.meta.strategy }} · {{ message.meta.policyVersion }} · Trace {{ message.meta.traceId.slice(0, 8) }}
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
            placeholder="请输入项目问题、报错信息或排障目标..."
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
    const knowledgeDocuments = ref([])
  const indexedKnowledgeDocuments = computed(() => knowledgeDocuments.value.filter(document => document.status === 'indexed'))
    const knowledgeSearchOpen = ref(false)
    const knowledgeQuery = ref('')
    const searchingKnowledge = ref(false)
    const knowledgeSearchResults = ref([])
    const knowledgeSearchDiagnostics = ref(null)
    const answeringKnowledge = ref(false)
    const knowledgeAnswer = ref(null)
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
        if (isStreaming.value) {

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
      }
    }


    async function handleStreaming(question) {
      const aiMessage = {
        role: 'assistant',
        content: '',
        meta: { status: 'streaming', strategy: '自动选择', policyVersion: '加载中', traceId: '' }
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
              strategy: payload.strategy || '自动选择',
              policyVersion: payload.policy_version || ''
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
              strategy: response.data.strategy || '自动选择', policyVersion: response.data.policy_version || '',
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
              strategy: response.data.strategy || '自动选择', policyVersion: response.data.policy_version || '',
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

    const answerKnowledge = async () => {
      const question = knowledgeQuery.value.trim()
      if (!question || answeringKnowledge.value) return
      answeringKnowledge.value = true
      knowledgeAnswer.value = null
      try {
        const response = await api.post('/knowledge/answer', { question, top_k: 5 })
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
      ElMessage.success(`文档 v${latest.version} 索引完成，活动版本已原子切换`)
      pendingVersionJob.value = null
    } else if (latest.status === 'failed') {
      ElMessage.warning(`文档 v${latest.version} 索引失败，旧版本保持可用（${latest.last_error_code || 'UNKNOWN'}）`)
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

    onMounted(() => {
      loadSessions()
      loadKnowledgeDocuments()
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
    indexedKnowledgeDocuments,
      knowledgeDocuments,
      knowledgeSearchOpen,
      knowledgeQuery,
      searchingKnowledge,
      knowledgeSearchResults,
      knowledgeSearchDiagnostics,
      answeringKnowledge,
      knowledgeAnswer,
      documentStatusLabel,
    jobStatusLabel,
      retrievalModeLabel,
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
      toggleKnowledgeSearch,
      searchKnowledge,
      answerKnowledge,
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

.routing-meta {
  margin-top: 9px;
  color: #8492a6;
  font-size: 11px;
  letter-spacing: 0.01em;
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
