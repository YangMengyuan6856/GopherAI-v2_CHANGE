import { createRouter, createWebHistory } from 'vue-router'
import Login from '../views/Login.vue'
import Register from '../views/Register.vue'
import AIChat from '../views/AIChat.vue'
import WorkspaceLayout from '../layouts/WorkspaceLayout.vue'
import Dashboard from '../views/Dashboard.vue'
import SystemSettings from '../views/SystemSettings.vue'

const routes = [
  {
    path: '/',
    redirect: '/login'
  },
  {
    path: '/login',
    name: 'Login',
    component: Login
  },
  {
    path: '/register',
    name: 'Register',
    component: Register
  },
  {
    path: '/menu',
    redirect: '/dashboard'
  },
  {
    path: '/ai-chat',
    redirect: '/dashboard/chat'
  },
  {
    path: '/dashboard',
    component: WorkspaceLayout,
    meta: { requiresAuth: true },
    children: [
      { path: '', name: 'Dashboard', component: Dashboard, meta: { title: '工作台主页' } },
      { path: 'chat', name: 'AIChat', component: AIChat, props: { workspace: 'chat' }, meta: { title: '统一智能对话' } },
      { path: 'history', name: 'ConversationHistory', component: AIChat, props: { workspace: 'history' }, meta: { title: '历史会话' } },
      { path: 'knowledge', name: 'KnowledgeWorkspace', component: AIChat, props: { workspace: 'knowledge' }, meta: { title: '证据检索与 RAG' } },
      { path: 'diagnostics', name: 'DiagnosticWorkspace', component: AIChat, props: { workspace: 'diagnostics' }, meta: { title: '故障诊断 Harness' } },
      { path: 'rca-experiment', name: 'RCAExperiment', component: () => import('../views/RCAExperiment.vue'), meta: { title: '自主排查与历史案例实验' } },
      { path: 'memory', name: 'MemoryWorkspace', component: AIChat, props: { workspace: 'memory' }, meta: { title: '三级记忆控制台' } },
      { path: 'tools', name: 'ToolRuntimeWorkspace', component: AIChat, props: { workspace: 'tools' }, meta: { title: '受治理工具运行时' } },
      { path: 'policy', name: 'PolicyWorkspace', component: AIChat, props: { workspace: 'policy' }, meta: { title: '策略演算与多 Agent' } },
      { path: 'evaluation', name: 'EvaluationWorkspace', component: AIChat, props: { workspace: 'evaluation' }, meta: { title: '评测与反馈闭环' } },
      { path: 'review', name: 'HumanReviewWorkspace', component: AIChat, props: { workspace: 'review' }, meta: { title: '人工验收工作台' } },
      { path: 'interview', name: 'InterviewWorkspace', component: AIChat, props: { workspace: 'interview' }, meta: { title: '面试导览' } },
      { path: 'settings', name: 'SystemSettings', component: SystemSettings, meta: { title: '系统设置' } }
    ]
  }
]

const router = createRouter({
  history: createWebHistory(process.env.BASE_URL),
  routes
})

router.beforeEach((to, from, next) => {
  const token = localStorage.getItem('token')
  if (to.matched.some(record => record.meta.requiresAuth) && !token) {
    next('/login')
  } else {
    next()
  }
})

export default router
