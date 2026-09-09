<template>
  <div class="control-shell">
    <aside class="global-sidebar" aria-label="全局导航">
      <router-link class="brand-mark" to="/dashboard" title="GopherAI 工作台">
        <span>G</span>
        <small>AI</small>
      </router-link>

      <nav class="primary-navigation">
        <router-link v-for="item in navigation" :key="item.path" :to="item.path" :title="item.label" :class="{ 'nav-active': isActive(item) }">
          <el-icon><component :is="item.icon" /></el-icon>
          <span>{{ item.label }}</span>
        </router-link>
      </nav>

      <button class="logout-button" type="button" title="退出登录" @click="handleLogout">
        <el-icon><SwitchButton /></el-icon>
        <span>退出</span>
      </button>
    </aside>

    <main class="workspace-frame">
      <header class="system-bar">
        <div>
          <span class="system-kicker">GOPHERAI · DEV SUPPORT CONTROL</span>
          <strong>{{ pageTitle }}</strong>
        </div>
        <div class="system-status">
          <span><i></i> API LINK</span>
          <span>SECURE SESSION</span>
        </div>
      </header>
      <section class="route-stage">
        <router-view v-slot="{ Component }">
          <transition name="workspace" mode="out-in">
            <component :is="Component" :key="$route.fullPath" />
          </transition>
        </router-view>
      </section>
    </main>
  </div>
</template>

<script>
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { ChatDotRound, Clock, House, Setting, SwitchButton } from '@element-plus/icons-vue'

export default {
  name: 'WorkspaceLayout',
  components: { SwitchButton },
  setup() {
    const route = useRoute()
    const router = useRouter()
    const navigation = [
      { path: '/dashboard', label: '工作台', icon: House },
      { path: '/dashboard/chat?new=1', label: '新聊天', icon: ChatDotRound },
      { path: '/dashboard/history', label: '历史会话', icon: Clock },
      { path: '/dashboard/settings', label: '系统设置', icon: Setting }
    ]
    const pageTitle = computed(() => route.meta.title || '工作台')
    const isActive = (item) => route.path === item.path.split('?')[0]

    const handleLogout = async () => {
      try {
        await ElMessageBox.confirm('确定退出当前安全会话吗？', '退出登录', {
          confirmButtonText: '退出',
          cancelButtonText: '取消',
          type: 'warning'
        })
        localStorage.removeItem('token')
        ElMessage.success('已退出登录')
        router.push('/login')
      } catch (_) {
        // User cancelled the explicit logout action.
      }
    }

    return { navigation, pageTitle, isActive, handleLogout }
  }
}
</script>

<style scoped>
.control-shell {
  min-height: 100vh;
  display: grid;
  grid-template-columns: 78px minmax(0, 1fr);
  overflow: hidden;
  color: var(--g-text-primary);
  background: var(--g-bg);
}

.global-sidebar {
  height: 100vh;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 28px;
  padding: 14px 8px 12px;
  border-right: 1px solid var(--g-border);
  background: #080f15;
  z-index: 20;
}

.brand-mark {
  width: 46px;
  height: 46px;
  display: grid;
  place-items: center;
  position: relative;
  text-decoration: none;
  border: 1px solid var(--g-primary);
  border-radius: 2px;
  color: #041015;
  background: var(--g-primary);
  box-shadow: 0 0 18px rgba(0, 229, 255, .2);
  font-family: var(--g-font-mono);
  font-size: 20px;
  font-weight: 900;
}

.brand-mark small {
  position: absolute;
  right: 3px;
  bottom: 1px;
  font-size: 7px;
}

.primary-navigation {
  width: 100%;
  display: grid;
  gap: 8px;
}

.primary-navigation a,
.logout-button {
  min-height: 58px;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 5px;
  border: 1px solid transparent;
  border-radius: 2px;
  color: var(--g-text-muted);
  background: transparent;
  text-decoration: none;
  font-size: 10px;
  cursor: pointer;
  transition: .18s ease;
}

.primary-navigation .el-icon,
.logout-button .el-icon { font-size: 20px; }

.primary-navigation a:hover,
.primary-navigation a.nav-active {
  color: var(--g-primary);
  border-color: var(--g-border-strong);
  background: rgba(0, 229, 255, .06);
  box-shadow: inset 2px 0 0 var(--g-primary);
}

.logout-button {
  width: 100%;
  margin-top: auto;
  font: inherit;
  font-size: 10px;
}

.logout-button:hover { color: var(--g-danger); }

.workspace-frame {
  height: 100vh;
  min-width: 0;
  display: grid;
  grid-template-rows: 48px minmax(0, 1fr);
}

.system-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 24px;
  padding: 0 24px;
  border-bottom: 1px solid var(--g-border);
  background: rgba(11, 19, 26, .94);
}

.system-bar > div:first-child {
  min-width: 0;
  display: flex;
  align-items: baseline;
  gap: 14px;
}

.system-kicker,
.system-status {
  color: var(--g-primary);
  font-family: var(--g-font-mono);
  font-size: 10px;
  letter-spacing: .12em;
}

.system-bar strong { font-size: 14px; }

.system-status { display: flex; gap: 18px; }
.system-status i {
  width: 6px;
  height: 6px;
  display: inline-block;
  margin-right: 6px;
  border-radius: 50%;
  background: var(--g-success);
  box-shadow: 0 0 8px var(--g-success);
}

.route-stage { min-height: 0; overflow: hidden; }
.workspace-enter-active, .workspace-leave-active { transition: opacity .16s ease; }
.workspace-enter-from, .workspace-leave-to { opacity: 0; }

@media (max-width: 760px) {
  .control-shell { grid-template-columns: 58px minmax(0, 1fr); }
  .global-sidebar { padding-inline: 4px; }
  .primary-navigation a span, .logout-button span { display: none; }
  .system-kicker, .system-status span:last-child { display: none; }
  .system-bar { padding-inline: 14px; }
}
</style>
