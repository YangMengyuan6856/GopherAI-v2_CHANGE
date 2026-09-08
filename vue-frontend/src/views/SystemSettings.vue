<template>
  <div class="settings-page">
    <header>
      <small>SYSTEM / PREFERENCES</small>
      <h1>系统设置</h1>
      <p>这里仅放置全局偏好与会话安全设置；业务能力配置仍由各自的独立控制台负责。</p>
    </header>
    <section class="settings-grid">
      <article>
        <div><strong>界面主题</strong><span>THEME</span></div>
        <p>深色工业控制台</p>
        <small>当前版本使用固定暗色主题，避免高饱和多色造成的视觉噪声。</small>
      </article>
      <article>
        <div><strong>策略安全边界</strong><span>POLICY</span></div>
        <p>Shadow / Recommend-only</p>
        <small>监控和演算只产生建议，不自动改写活动策略或执行修复工具。</small>
      </article>
      <article>
        <div><strong>身份与权限</strong><span>SESSION</span></div>
        <p>登录令牌已由浏览器隔离保存</p>
        <small>退出登录会清理本地令牌，并跳转到登录页。</small>
        <button type="button" @click="logout">退出当前账号</button>
      </article>
    </section>
  </div>
</template>

<script>
import { useRouter } from 'vue-router'

export default {
  name: 'SystemSettings',
  setup() {
    const router = useRouter()
    const logout = () => {
      localStorage.removeItem('token')
      router.push('/login')
    }
    return { logout }
  }
}
</script>

<style scoped>
.settings-page { height: 100%; overflow-y: auto; padding: 34px clamp(20px, 4vw, 54px); background: var(--g-bg); }
header { max-width: 760px; margin-bottom: 28px; }
header small { color: var(--g-primary); font: 10px var(--g-font-mono); letter-spacing: .1em; }
h1 { margin: 10px 0; font-size: 32px; }
header p, article small { color: var(--g-text-secondary); line-height: 1.7; }
.settings-grid { max-width: 1000px; display: grid; gap: 12px; }
article { padding: 22px; border: 1px solid var(--g-border); border-radius: 2px; background: var(--g-panel); }
article > div { display: flex; justify-content: space-between; gap: 20px; }
article > div span { color: var(--g-primary); font: 10px var(--g-font-mono); }
article p { margin: 16px 0 6px; color: var(--g-text-primary); font-family: var(--g-font-mono); }
button { margin-top: 18px; padding: 9px 14px; border: 1px solid var(--g-danger); border-radius: 2px; color: var(--g-danger); background: transparent; cursor: pointer; }
</style>
