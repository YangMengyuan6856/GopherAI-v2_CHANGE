<template>
  <div id="app">
    <router-view v-slot="{ Component }">
      <transition name="page" mode="out-in">
        <component :is="Component" />
      </transition>
    </router-view>
  </div>
</template>

<script>
export default {
  name: 'App'
}
</script>

<style>
:root {
  --el-color-primary: #00e5ff;
  --el-color-primary-light-3: #3bedff;
  --el-color-primary-light-5: #72f2ff;
  --el-color-primary-light-7: #a9f8ff;
  --el-color-primary-light-8: #c5fbff;
  --el-color-primary-light-9: #e2fdff;
  --el-color-primary-dark-2: #00b7cc;
  --el-bg-color: #161f2b;
  --el-bg-color-overlay: #182430;
  --el-fill-color-blank: #161f2b;
  --el-fill-color-light: #1b2935;
  --el-text-color-primary: #f5fbff;
  --el-text-color-regular: #a0b2c6;
  --el-text-color-secondary: #74889c;
  --el-border-color: #1c2e40;
  --el-border-color-light: #22384a;
  --el-border-radius-base: 2px;
  --g-bg: #0b131a;
  --g-panel: #111b25;
  --g-panel-raised: #161f2b;
  --g-border: #1c2e40;
  --g-border-strong: #28465c;
  --g-primary: #00e5ff;
  --g-primary-soft: rgba(0, 229, 255, .08);
  --g-text-primary: #f5fbff;
  --g-text-secondary: #a0b2c6;
  --g-text-muted: #688096;
  --g-success: #20e3a2;
  --g-warning: #ffb84d;
  --g-danger: #ff667a;
  --g-font-mono: Consolas, "Fira Code", "SFMono-Regular", monospace;
}

* {
  margin: 0;
  padding: 0;
  box-sizing: border-box;
}

html, body {
  height: 100%;
  font-family: 'Helvetica Neue', Helvetica, 'PingFang SC', 'Hiragino Sans GB', 'Microsoft YaHei', '微软雅黑', Arial, sans-serif;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
  color: var(--g-text-primary);
  background: var(--g-bg);
}

#app {
  height: 100%;
}

/* 页面切换动画 */
.page-enter-active,
.page-leave-active {
  transition: all 0.4s cubic-bezier(0.55, 0, 0.1, 1);
}

.page-enter-from {
  opacity: 0;
  transform: translateX(30px);
}

.page-leave-to {
  opacity: 0;
  transform: translateX(-30px);
}

/* 全局滚动条样式 */
::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}

::-webkit-scrollbar-track {
  background: #0a1118;
}

::-webkit-scrollbar-thumb {
  background: #28465c;
  border-radius: 2px;
  transition: background 0.3s ease;
}

::-webkit-scrollbar-thumb:hover {
  background: #00a9bd;
}

/* Element Plus 组件样式覆盖 */
.el-button {
  font-weight: 500;
  border-radius: 2px;
}

.el-input {
  border-radius: 2px;
}

.el-card {
  border-radius: 2px;
}

.el-message {
  border-radius: 2px;
  border-color: var(--g-border-strong);
  background: var(--g-panel-raised);
}

.el-message-box {
  border: 1px solid var(--g-border-strong);
  border-radius: 2px;
  background: var(--g-panel-raised);
}

/* 响应式设计 */
@media (max-width: 768px) {
  .page-enter-from,
  .page-leave-to {
    transform: translateX(0);
    opacity: 0;
  }

  .page-enter-active,
  .page-leave-active {
    transition: opacity 0.3s ease;
  }
}
</style>
