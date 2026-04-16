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
        <label for="modelType">选择模型：</label>
        <select id="modelType" v-model="selectedModel" class="model-select">
          <option value="1">阿里百炼</option>
          <option value="2">阿里百炼 RAG</option>
          <option value="3">阿里百炼 MCP</option>
          <option value="5">ReAct 推理</option>
        </select>
        <label for="streamingMode" style="margin-left: 20px;">
          <input type="checkbox" id="streamingMode" v-model="isStreaming" />
          流式响应
        </label>
        <button class="upload-btn" @click="triggerFileUpload" :disabled="uploading">📎 上传文档(.md/.txt)</button>
        <input
          ref="fileInput"
          type="file"
          accept=".md,.txt,text/markdown,text/plain"
          style="display: none"
          @change="handleFileUpload"
        />
        <button class="skill-btn" @click="toggleSkillPanel">🛠 技能管理</button>
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
        </div>
      </div>

      <div class="chat-input">
        <div class="input-wrapper">
          <textarea
            v-model="inputMessage"
            placeholder="请输入你的问题...  输入 /skill 查看可用技能"
            @keydown.enter.exact.prevent="sendMessage"
            @input="onInputChange"
            :disabled="loading"
            ref="messageInput"
            rows="1"
          ></textarea>
          <div v-if="showSkillHint && skillSuggestions.length" class="skill-hint-popup">
            <div class="skill-hint-title">可用技能（输入 /skill &lt;code&gt; 使用）</div>
            <div
              v-for="s in skillSuggestions"
              :key="s.code"
              class="skill-hint-item"
              @click="insertSkillCommand(s.code)"
            >
              <span class="skill-hint-code">/skill {{ s.code }}</span>
              <span class="skill-hint-desc">{{ s.description }}</span>
            </div>
          </div>
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

    <!-- 技能管理侧边面板 -->
    <transition name="slide">
      <div v-if="skillPanelVisible" class="skill-panel">
        <div class="skill-panel-header">
          <h3>技能管理</h3>
          <button class="skill-panel-close" @click="toggleSkillPanel">✕</button>
        </div>
        <div class="skill-panel-body">
          <div v-if="skillsLoading" class="skill-loading">加载中...</div>
          <div v-else-if="allSkills.length === 0" class="skill-empty">暂无可用技能</div>
          <div v-else>
            <div v-for="s in allSkills" :key="s.code" class="skill-card">
              <div class="skill-card-info">
                <div class="skill-card-name">{{ s.name }}</div>
                <div class="skill-card-code">/skill {{ s.code }}</div>
                <div class="skill-card-desc">{{ s.description }}</div>
              </div>
              <label class="skill-toggle">
                <input
                  type="checkbox"
                  :checked="enabledSkillCodes.includes(s.code)"
                  @change="toggleSkill(s.code, $event.target.checked)"
                />
                <span class="skill-toggle-slider"></span>
              </label>
            </div>
          </div>
        </div>
        <div class="skill-panel-footer">
          <button class="skill-refresh-btn" @click="loadSkillData">刷新</button>
        </div>
      </div>
    </transition>
  </div>
</template>

<script>


import { ref, nextTick, computed, onMounted } from 'vue'
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
    const selectedModel = ref('1')
    const isStreaming = ref(false)
    const uploading = ref(false)
    const fileInput = ref(null)

    // 技能管理相关
    const skillPanelVisible = ref(false)
    const allSkills = ref([])
    const enabledSkillCodes = ref([])
    const skillsLoading = ref(false)
    const showSkillHint = ref(false)
    const skillSuggestions = ref([])

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
        meta: { status: 'streaming' } // mark streaming
      }


      const aiMessageIndex = currentMessages.value.length
      currentMessages.value.push(aiMessage)

      if (!tempSession.value && currentSessionId.value && sessions.value[currentSessionId.value]) {
        if (!sessions.value[currentSessionId.value].messages) sessions.value[currentSessionId.value].messages = []
        sessions.value[currentSessionId.value].messages.push({ role: 'assistant', content: '' })
      }


      const url = tempSession.value
        ? '/api/AI/chat/send-stream-new-session'  
        : '/api/AI/chat/send-stream'           

      const headers = {
        'Content-Type': 'application/json',
        'Authorization': `Bearer ${localStorage.getItem('token') || ''}`
      }

      const body = tempSession.value
        ? { question: question, modelType: selectedModel.value }
        : { question: question, modelType: selectedModel.value, sessionId: currentSessionId.value }

      try {
        // 创建 fetch 连接读取 SSE 流
        const response = await fetch(url, {
          method: 'POST',
          headers,
          body: JSON.stringify(body)
        })

        if (!response.ok) {
          loading.value = false
          throw new Error('Network response was not ok')
        }

        const reader = response.body.getReader()
        const decoder = new TextDecoder()
        let buffer = ''

        // 读取流数据
        // eslint-disable-next-line no-constant-condition
        while (true) {
          const { done, value } = await reader.read()
          if (done) break

          const chunk = decoder.decode(value, { stream: true })
          buffer += chunk

          // 按行分割
          const lines = buffer.split('\n')
          buffer = lines.pop() || '' // 保留未完成的行

          for (const line of lines) {
            const trimmedLine = line.trim()
            if (!trimmedLine) continue

            // 处理 SSE 格式：data: <content>
            if (trimmedLine.startsWith('data:')) {
              const data = trimmedLine.slice(5).trim()
              console.log('[SSE] Received:', data) // 调试日志

              if (data === '[DONE]') {
                // 流结束
                console.log('[SSE] Stream done')
                loading.value = false
                currentMessages.value[aiMessageIndex].meta = { status: 'done' }
                currentMessages.value = [...currentMessages.value]
              } else if (data.startsWith('{')) {
                // 尝试解析 JSON（如 sessionId）
                try {
                  const parsed = JSON.parse(data)
                  if (parsed.sessionId) {
                    const newSid = String(parsed.sessionId)
                    console.log('[SSE] Session ID:', newSid)
                    if (tempSession.value) {
                      sessions.value[newSid] = {
                        id: newSid,
                        name: '新会话',
                        messages: [...currentMessages.value]
                      }
                      currentSessionId.value = newSid
                      tempSession.value = false
                    }
                  }
                } catch (e) {
                  // 不是 JSON，当作普通文本处理
                  currentMessages.value[aiMessageIndex].content += data
                  console.log('[SSE] Content updated:', currentMessages.value[aiMessageIndex].content.length)
                }
              } else {
                // 普通文本数据，直接追加
                // 使用数组索引直接更新，强制 Vue 响应式系统检测变化
                currentMessages.value[aiMessageIndex].content += data
                console.log('[SSE] Content updated:', currentMessages.value[aiMessageIndex].content.length)
              }

              // 每收到一条数据就立即更新 DOM
              // 强制更新整个数组以触发响应式
              currentMessages.value = [...currentMessages.value]
              
              // 使用 requestAnimationFrame 强制浏览器重排
              await new Promise(resolve => {
                requestAnimationFrame(() => {
                  scrollToBottom()
                  resolve()
                })
              })
            }
          }
        }

        // 流读取完成后的处理
        loading.value = false
        currentMessages.value[aiMessageIndex].meta = { status: 'done' }
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
        currentMessages.value[aiMessageIndex].meta = { status: 'error' }
        currentMessages.value = [...currentMessages.value]
        ElMessage.error('流式传输出错')
      }
    }


    async function handleNormal(question) {
      if (tempSession.value) {

        const response = await api.post('/AI/chat/send-new-session', {
          question: question,
          modelType: selectedModel.value
        })
        if (response.data && response.data.status_code === 1000) {
          const sessionId = String(response.data.sessionId)
          const aiMessage = {
            role: 'assistant',
            content: response.data.Information || ''
          }

          sessions.value[sessionId] = {
            id: sessionId,
            name: '新会话',
            messages: [ { role: 'user', content: question }, aiMessage ]
          }
          currentSessionId.value = sessionId
          tempSession.value = false
          currentMessages.value = [...sessions.value[sessionId].messages]
        } else {
          ElMessage.error(response.data?.status_msg || '发送失败')

          currentMessages.value.pop()
        }
      } else {

        const sessionMsgs = sessions.value[currentSessionId.value].messages

        sessionMsgs.push({ role: 'user', content: question })

        const response = await api.post('/AI/chat/send', {
          question: question,
          modelType: selectedModel.value,
          sessionId: currentSessionId.value
        })
        if (response.data && response.data.status_code === 1000) {
          const aiMessage = { role: 'assistant', content: response.data.Information || '' }
          sessionMsgs.push(aiMessage)
          currentMessages.value = [...sessionMsgs]
        } else {
          ElMessage.error(response.data?.status_msg || '发送失败')
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

    const handleFileUpload = async (event) => {
      const file = event.target.files[0]
      if (!file) return

      // 前端校验：只允许.md或.txt文件
      const fileName = file.name.toLowerCase()
      if (!fileName.endsWith('.md') && !fileName.endsWith('.txt')) {
        ElMessage.error('只允许上传 .md 或 .txt 文件')
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

        const response = await api.post('/file/upload', formData, {
          headers: {
            'Content-Type': 'multipart/form-data'
          }
        })

        if (response.data && response.data.status_code === 1000) {
          ElMessage.success(`文件上传成功`)
        } else {
          ElMessage.error(response.data?.status_msg || '上传失败')
        }
      } catch (error) {
        console.error('File upload error:', error)
        ElMessage.error('文件上传失败')
      } finally {
        uploading.value = false
        // 清空文件输入
        if (fileInput.value) {
          fileInput.value.value = ''
        }
      }
    }

    // ---- 技能管理方法 ----

    const toggleSkillPanel = () => {
      skillPanelVisible.value = !skillPanelVisible.value
      if (skillPanelVisible.value) {
        loadSkillData()
      }
    }

    const loadSkillData = async () => {
      skillsLoading.value = true
      try {
        const [listRes, userRes] = await Promise.all([
          api.get('/skill/list'),
          api.get('/skill/user')
        ])
        if (listRes.data && listRes.data.status_code === 1000) {
          allSkills.value = listRes.data.skills || []
        }
        if (userRes.data && userRes.data.status_code === 1000) {
          enabledSkillCodes.value = userRes.data.skill_codes || []
        }
      } catch (err) {
        console.error('Load skills error:', err)
        ElMessage.error('加载技能列表失败')
      } finally {
        skillsLoading.value = false
      }
    }

    const toggleSkill = async (code, enabled) => {
      try {
        const url = enabled ? '/skill/enable' : '/skill/disable'
        const res = await api.post(url, { skill_code: code })
        if (res.data && res.data.status_code === 1000) {
          if (enabled) {
            if (!enabledSkillCodes.value.includes(code)) {
              enabledSkillCodes.value.push(code)
            }
          } else {
            enabledSkillCodes.value = enabledSkillCodes.value.filter(c => c !== code)
          }
          ElMessage.success(enabled ? `已启用技能 [${code}]` : `已禁用技能 [${code}]`)
        } else {
          ElMessage.error(res.data?.status_msg || '操作失败')
          await loadSkillData()
        }
      } catch (err) {
        console.error('Toggle skill error:', err)
        ElMessage.error('操作失败')
        await loadSkillData()
      }
    }

    const onInputChange = () => {
      const val = inputMessage.value.trim()
      if (val === '/skill' || val === '/skill ') {
        showSkillHint.value = true
        skillSuggestions.value = allSkills.value.length > 0
          ? allSkills.value
          : []
        if (allSkills.value.length === 0) {
          api.get('/skill/list').then(res => {
            if (res.data && res.data.status_code === 1000) {
              allSkills.value = res.data.skills || []
              skillSuggestions.value = allSkills.value
            }
          })
        }
      } else {
        showSkillHint.value = false
      }
    }

    const insertSkillCommand = (code) => {
      inputMessage.value = `/skill ${code} `
      showSkillHint.value = false
      nextTick(() => {
        if (messageInput.value) messageInput.value.focus()
      })
    }

    onMounted(() => {
      loadSessions()
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
      selectedModel,
      isStreaming,
      uploading,
      fileInput,
      skillPanelVisible,
      allSkills,
      enabledSkillCodes,
      skillsLoading,
      showSkillHint,
      skillSuggestions,
      renderMarkdown,
      playTTS,
      createNewSession,
      switchSession,
      syncHistory,
      sendMessage,
      triggerFileUpload,
      handleFileUpload,
      toggleSkillPanel,
      loadSkillData,
      toggleSkill,
      onInputChange,
      insertSkillCommand
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

.model-select {
  margin-left: 6px;
  padding: 6px 10px;
  border: 1px solid rgba(0, 0, 0, 0.06);
  border-radius: 8px;
  background: white;
  color: #2c3e50;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
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

/* 技能管理按钮 */
.skill-btn {
  background: linear-gradient(135deg, #6366f1 0%, #8b5cf6 100%);
  color: white;
  padding: 8px 14px;
  border: none;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 600;
  box-shadow: 0 4px 12px rgba(99, 102, 241, 0.2);
  transition: all 0.2s ease;
}

.skill-btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 6px 16px rgba(99, 102, 241, 0.3);
}

/* 技能管理侧边面板 */
.skill-panel {
  position: absolute;
  top: 0;
  right: 0;
  width: 340px;
  height: 100vh;
  background: rgba(255, 255, 255, 0.98);
  backdrop-filter: blur(15px);
  box-shadow: -4px 0 30px rgba(0, 0, 0, 0.12);
  display: flex;
  flex-direction: column;
  z-index: 100;
  border-left: 1px solid rgba(0, 0, 0, 0.06);
}

.slide-enter-active,
.slide-leave-active {
  transition: transform 0.3s ease;
}
.slide-enter-from,
.slide-leave-to {
  transform: translateX(100%);
}

.skill-panel-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 20px 24px;
  border-bottom: 1px solid rgba(0, 0, 0, 0.06);
  background: linear-gradient(135deg, rgba(99, 102, 241, 0.06) 0%, rgba(139, 92, 246, 0.06) 100%);
}

.skill-panel-header h3 {
  margin: 0;
  font-size: 18px;
  font-weight: 700;
  color: #2c3e50;
}

.skill-panel-close {
  background: none;
  border: none;
  font-size: 18px;
  cursor: pointer;
  color: #999;
  padding: 4px 8px;
  border-radius: 6px;
  transition: all 0.2s ease;
}

.skill-panel-close:hover {
  background: rgba(0, 0, 0, 0.06);
  color: #333;
}

.skill-panel-body {
  flex: 1;
  overflow-y: auto;
  padding: 16px;
}

.skill-loading,
.skill-empty {
  text-align: center;
  color: #999;
  padding: 40px 0;
  font-size: 14px;
}

.skill-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px;
  margin-bottom: 12px;
  background: white;
  border-radius: 12px;
  border: 1px solid rgba(0, 0, 0, 0.06);
  box-shadow: 0 2px 8px rgba(0, 0, 0, 0.04);
  transition: all 0.2s ease;
}

.skill-card:hover {
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.08);
  transform: translateY(-1px);
}

.skill-card-info {
  flex: 1;
  min-width: 0;
}

.skill-card-name {
  font-weight: 600;
  font-size: 15px;
  color: #2c3e50;
  margin-bottom: 4px;
}

.skill-card-code {
  font-family: 'Courier New', monospace;
  font-size: 12px;
  color: #6366f1;
  background: rgba(99, 102, 241, 0.08);
  padding: 2px 8px;
  border-radius: 4px;
  display: inline-block;
  margin-bottom: 6px;
}

.skill-card-desc {
  font-size: 12px;
  color: #7f8c8d;
  line-height: 1.4;
}

/* 开关样式 */
.skill-toggle {
  position: relative;
  display: inline-block;
  width: 44px;
  height: 24px;
  flex-shrink: 0;
  margin-left: 12px;
}

.skill-toggle input {
  opacity: 0;
  width: 0;
  height: 0;
}

.skill-toggle-slider {
  position: absolute;
  cursor: pointer;
  top: 0;
  left: 0;
  right: 0;
  bottom: 0;
  background: #ccc;
  border-radius: 24px;
  transition: 0.3s;
}

.skill-toggle-slider::before {
  content: '';
  position: absolute;
  height: 18px;
  width: 18px;
  left: 3px;
  bottom: 3px;
  background: white;
  border-radius: 50%;
  transition: 0.3s;
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.15);
}

.skill-toggle input:checked + .skill-toggle-slider {
  background: linear-gradient(135deg, #6366f1 0%, #8b5cf6 100%);
}

.skill-toggle input:checked + .skill-toggle-slider::before {
  transform: translateX(20px);
}

.skill-panel-footer {
  padding: 16px;
  border-top: 1px solid rgba(0, 0, 0, 0.06);
}

.skill-refresh-btn {
  width: 100%;
  padding: 10px;
  background: linear-gradient(135deg, #6366f1 0%, #8b5cf6 100%);
  color: white;
  border: none;
  border-radius: 10px;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
  box-shadow: 0 4px 12px rgba(99, 102, 241, 0.2);
}

.skill-refresh-btn:hover {
  transform: translateY(-2px);
  box-shadow: 0 6px 16px rgba(99, 102, 241, 0.3);
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

/* 技能提示弹出框 */
.skill-hint-popup {
  position: absolute;
  bottom: 100%;
  left: 0;
  right: 0;
  background: white;
  border-radius: 12px;
  box-shadow: 0 -4px 24px rgba(0, 0, 0, 0.12);
  border: 1px solid rgba(0, 0, 0, 0.06);
  margin-bottom: 8px;
  max-height: 260px;
  overflow-y: auto;
  z-index: 50;
  animation: hintSlideUp 0.2s ease-out;
}

@keyframes hintSlideUp {
  from { opacity: 0; transform: translateY(8px); }
  to { opacity: 1; transform: translateY(0); }
}

.skill-hint-title {
  padding: 10px 16px;
  font-size: 12px;
  color: #999;
  border-bottom: 1px solid rgba(0, 0, 0, 0.06);
  font-weight: 600;
}

.skill-hint-item {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 16px;
  cursor: pointer;
  transition: background 0.15s ease;
}

.skill-hint-item:hover {
  background: rgba(99, 102, 241, 0.06);
}

.skill-hint-code {
  font-family: 'Courier New', monospace;
  font-size: 13px;
  color: #6366f1;
  font-weight: 600;
  white-space: nowrap;
}

.skill-hint-desc {
  font-size: 12px;
  color: #7f8c8d;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
