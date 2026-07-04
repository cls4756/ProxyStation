<template>
  <div v-if="authChecking" class="auth-screen">
    <div class="auth-card">
      <div class="auth-title">ProxyStation</div>
      <div class="auth-subtitle">正在检查登录状态...</div>
    </div>
  </div>

  <div v-else-if="!authenticated" class="auth-screen">
    <form class="auth-card" @submit.prevent="handleLogin">
      <div class="auth-title">ProxyStation</div>
      <div class="auth-subtitle">请输入管理端用户名和密码</div>
      <input class="input auth-input" v-model="loginForm.username" placeholder="用户名" autocomplete="username" />
      <input class="input auth-input" v-model="loginForm.password" type="password" placeholder="密码" autocomplete="current-password" />
      <button class="btn btn-primary auth-submit" :disabled="loginLoading">
        {{ loginLoading ? '登录中...' : '登录' }}
      </button>
      <div v-if="authError" class="auth-error">{{ authError }}</div>
    </form>
  </div>

  <div v-else id="app">
    <!-- 顶部 Navbar，完全对齐 v2rayA 风格 -->
    <nav class="navbar">
      <div class="navbar-left">
        <span class="brand">
          <span class="brand-icon">🛰</span>
          <span class="brand-name">ProxyStation</span>
        </span>

        <!-- 运行状态标签 -->
        <span
          :class="['status-tag', store.running ? 'status-running' : 'status-stopped']"
          @click="handleToggleProxy()"
          title="点击切换代理状态"
        >
          {{ store.running ? '运行中' : '未运行' }}
        </span>
        <!-- 启动错误提示 -->
        <span v-if="startError" class="start-error" :title="startError">
          ⚠ {{ startError }}
          <span style="cursor:pointer; margin-left:4px" @click="startError=''">✕</span>
        </span>

        <!-- 出站选择下拉 -->
        <div class="outbound-dropdown" v-if="store.outbounds.length">
          <span class="outbound-tag" @click="showOutboundMenu = !showOutboundMenu">
            {{ currentOutboundLabel }}
            <span class="caret">▾</span>
          </span>
          <div class="dropdown-menu" v-if="showOutboundMenu" @mouseleave="showOutboundMenu = false">
            <div
              v-for="o in store.outbounds"
              :key="o.name"
              :class="['dropdown-item', o.name === currentOutbound ? 'active' : '']"
            >
              <span @click="selectOutbound(o)" style="flex:1">
                {{ o.name }}
                <span class="outbound-target-badge" v-if="o.target?.targetType">
                  <template v-if="o.target.targetType === 'group'">
                    📁 {{ getGroupName(o.target.groupId) }}
                  </template>
                  <template v-else-if="o.target.targetType === 'node' && o.target.nodeRef">
                    🔗 {{ getNodeName(o.target.nodeRef) }}
                  </template>
                </span>
              </span>
              <span class="dd-edit-btn" @click.stop="openOutboundEdit(o); showOutboundMenu = false" title="配置">⚙</span>
              <span
                v-if="o.name !== 'proxy'"
                class="dd-del-btn"
                @click.stop="deleteOutbound(o.name); showOutboundMenu = false"
                title="删除"
              >✕</span>
            </div>
            <div class="dropdown-divider"></div>
            <div class="dropdown-item" @click="showCreateOutbound = true; showOutboundMenu = false">
              ＋ 新建出站
            </div>
          </div>
        </div>

        <!-- 新建出站弹窗 -->
        <div class="modal-overlay" v-if="showCreateOutbound" @mousedown.self="showCreateOutbound = false">
          <div class="modal-box" style="width:360px">
            <div class="modal-header">
              <span class="modal-title">新建出站</span>
              <span class="modal-close" @click="showCreateOutbound = false">✕</span>
            </div>
            <div class="modal-body">
              <div class="form-group">
                <label class="form-label">出站名称</label>
                <input class="input" v-model="newOutboundName" placeholder="my-outbound" @keyup.enter="createOutbound" autofocus />
              </div>
            </div>
            <div class="modal-footer">
              <button class="btn btn-light" @click="showCreateOutbound = false">取消</button>
              <button class="btn btn-primary" :disabled="creatingOutbound" @click="createOutbound">
                {{ creatingOutbound ? '创建中...' : '创建' }}
              </button>
            </div>
          </div>
        </div>

        <!-- 配置出站弹窗 -->
        <div class="modal-overlay" v-if="editingOutbound" @mousedown.self="editingOutbound = null">
          <div class="modal-box" style="width:480px">
            <div class="modal-header">
              <span class="modal-title">配置出站：{{ editingOutbound.name }}</span>
              <span class="modal-close" @click="editingOutbound = null">✕</span>
            </div>
            <div class="modal-body">
              <div class="form-group">
                <label class="form-label">目标类型</label>
                <div style="display:flex; gap:16px">
                  <label style="display:flex; align-items:center; gap:6px; cursor:pointer; font-size:13px">
                    <input type="radio" v-model="outboundForm.targetType" value="node" /> 指定节点
                  </label>
                  <label style="display:flex; align-items:center; gap:6px; cursor:pointer; font-size:13px">
                    <input type="radio" v-model="outboundForm.targetType" value="group" /> 指定分组（自动选优）
                  </label>
                </div>
              </div>
              <template v-if="outboundForm.targetType === 'node'">
                <div class="form-group">
                  <label class="form-label">选择节点</label>
                  <select class="input" v-model="outboundForm.nodeKey">
                    <option value="">-- 请选择 --</option>
                    <optgroup label="手动节点">
                      <option v-for="(s, i) in editServers" :key="`s-${i}`" :value="`server:${i}:0`">
                        [{{ s.type }}] {{ s.name || '未命名' }} — {{ s.host }}:{{ s.port }}
                      </option>
                    </optgroup>
                    <optgroup v-for="(sub, si) in editSubscriptions" :key="sub.id" :label="sub.name">
                      <option v-for="(s, i) in sub.servers" :key="`ss-${si}-${i}`" :value="`sub_server:${i}:${si}`">
                        [{{ s.type }}] {{ s.name || '未命名' }} — {{ s.host }}:{{ s.port }}
                      </option>
                    </optgroup>
                  </select>
                </div>
              </template>
              <template v-if="outboundForm.targetType === 'group'">
                <div class="form-group">
                  <label class="form-label">选择分组</label>
                  <select class="input" v-model="outboundForm.groupId">
                    <option value="">-- 请选择 --</option>
                    <option v-for="g in editGroups" :key="g.id" :value="g.id">
                      {{ g.name }}（{{ g.servers?.length || 0 }} 个节点）
                    </option>
                  </select>
                </div>
                <div class="form-group">
                  <label class="form-label">策略</label>
                  <select class="input" v-model="outboundForm.mode">
                    <option value="leastping">最低延迟（自动切换）</option>
                    <option value="roundrobin">轮询</option>
                  </select>
                </div>
                <div class="form-group">
                  <label class="form-label">探测间隔</label>
                  <input class="input" v-model="outboundForm.probeInterval" placeholder="30s" />
                </div>
              </template>
            </div>
            <div class="modal-footer">
              <button class="btn btn-light" style="color:#f14668; margin-right:auto" @click="disconnectOutbound">断开</button>
              <button class="btn btn-light" @click="editingOutbound = null">取消</button>
              <button class="btn btn-primary" :disabled="savingOutbound" @click="saveOutbound">
                {{ savingOutbound ? '保存中...' : '确认' }}
              </button>
            </div>
          </div>
        </div>
      </div>

      <div class="navbar-right">
        <router-link to="/" class="nav-btn" active-class="nav-btn-active">节点</router-link>
        <router-link to="/settings" class="nav-btn" active-class="nav-btn-active">设置</router-link>
        <div class="user-menu" ref="userMenuRef">
          <button class="user-menu-trigger" @click="showUserMenu = !showUserMenu">
            <span class="auth-user">{{ authUsername }}</span>
            <span class="caret">▾</span>
          </button>
          <div v-if="showUserMenu" class="user-dropdown-menu">
            <button class="user-dropdown-item" @click="openAccountModal">账号设置</button>
            <button class="user-dropdown-item user-dropdown-danger" @click="handleLogoutFromMenu">退出登录</button>
          </div>
        </div>
      </div>
    </nav>

    <!-- 主内容 -->
    <main class="main-content">
      <!-- 出站状态条（全局，显示在所有页面顶部） -->
      <div class="global-status-bar" v-if="proxyTarget">
        <template v-if="proxyTarget.targetType === 'group'">
          <span class="gsb-icon">📁</span>
          <span>分组模式：<strong>{{ getGroupName(proxyTarget.groupId) }}</strong></span>
          <span class="gsb-sep">·</span>
          <span>{{ proxyTarget.mode || 'leastping' }}</span>
          <span class="gsb-sep">·</span>
          <span v-if="proxyTarget.activeNodeRef" style="color:#52c41a">
            当前：{{ getNodeName(proxyTarget.activeNodeRef) }}
          </span>
          <span v-else style="color:#faad14">等待探测…</span>
          <button class="gsb-btn" @click="openOutboundEdit(store.outbounds.find(o=>o.name===currentOutbound))">修改</button>
        </template>
        <template v-else-if="proxyTarget.targetType === 'node' && proxyTarget.nodeRef">
          <span class="gsb-icon">🔗</span>
          <span>手动节点：<strong>{{ getNodeName(proxyTarget.nodeRef) }}</strong></span>
          <button class="gsb-btn" @click="openOutboundEdit(store.outbounds.find(o=>o.name===currentOutbound))">切换为分组模式</button>
        </template>
      </div>
      <div class="global-status-bar gsb-empty" v-else>
        <span>未配置出站，点击节点行的「连接」按钮直接连接，或</span>
        <button class="gsb-btn" @click="openOutboundEdit(store.outbounds.find(o=>o.name===currentOutbound))">配置分组模式</button>
      </div>
      <router-view />
    </main>

    <!-- 日志面板（底部可折叠） -->
    <div class="logs-panel" :class="{ 'logs-panel-expanded': showLogs }" ref="logsPanel">
      <div class="logs-resize-handle" v-if="showLogs" @mousedown="startResize"></div>
      <div class="logs-header" @click="showLogs = !showLogs">
        <span class="logs-title">📋 日志</span>
        <span class="logs-toggle">{{ showLogs ? '▼' : '▲' }}</span>
      </div>
      <div class="logs-content" v-if="showLogs">
        <div class="logs-list" ref="logsContainer">
          <div v-for="(log, i) in logs" :key="i" :class="['log-line', `log-${log.level || 'info'}`]">
            <span class="log-time">{{ log.time }}</span>
            <span class="log-message">{{ log.message }}</span>
          </div>
          <div v-if="!logs.length" class="log-empty">暂无日志</div>
        </div>
        <div class="logs-actions">
          <button class="btn btn-sm btn-light" @click="clearLogs">清空</button>
          <button class="btn btn-sm" :class="logsAutoScroll ? 'btn-success' : 'btn-light'" @click="toggleAutoScroll" title="打开自动滚动到最新日志">
            {{ logsAutoScroll ? '📌 自动滚动' : '📍 手动滚动' }}
          </button>
          <button class="btn btn-sm" :class="logsStreaming ? 'btn-success' : 'btn-light'" @click="toggleLogsStream">
            {{ logsStreaming ? '● 实时' : '○ 暂停' }}
          </button>
        </div>
      </div>
    </div>

    <!-- 全局导入弹窗 -->
    <ImportModal v-if="showImport" @close="showImport = false" @done="onImportDone" />

    <div class="modal-overlay" v-if="showAccountModal" @mousedown.self="closeAccountModal">
      <div class="modal-box" style="width:480px">
        <div class="modal-header">
          <span class="modal-title">账号设置</span>
          <span class="modal-close" @click="closeAccountModal">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">用户名</label>
            <input class="input" v-model="accountForm.webUsername" placeholder="admin" />
          </div>
          <div class="form-group">
            <button class="btn btn-primary" :disabled="accountSaving" @click="saveAccountProfile">
              {{ accountSaving ? '保存中...' : '保存用户名' }}
            </button>
          </div>
          <div v-if="accountMsg" :class="['account-msg', accountMsg.error ? 'account-msg-error' : 'account-msg-success']">
            {{ accountMsg.text }}
          </div>

          <div class="account-divider"></div>

          <div class="form-group">
            <label class="form-label">旧密码</label>
            <input type="password" class="input" v-model="passwordForm.oldPassword" placeholder="当前密码" />
          </div>
          <div class="form-group">
            <label class="form-label">新密码</label>
            <input type="password" class="input" v-model="passwordForm.newPassword" placeholder="新密码" />
          </div>
          <div class="form-group">
            <label class="form-label">确认新密码</label>
            <input type="password" class="input" v-model="passwordForm.confirmPassword" placeholder="再次输入新密码" />
          </div>
          <div class="form-group">
            <button class="btn btn-light" :disabled="passwordSaving" @click="changePassword">
              {{ passwordSaving ? '修改中...' : '修改密码' }}
            </button>
          </div>
          <div v-if="passwordMsg" :class="['account-msg', passwordMsg.error ? 'account-msg-error' : 'account-msg-success']">
            {{ passwordMsg.text }}
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="closeAccountModal">关闭</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onBeforeUnmount, provide, watch, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { useProxyStore } from './stores/proxy'
import { api, setUnauthorizedHandler } from './api'
import ImportModal from './components/ImportModal.vue'

const store = useProxyStore()
const router = useRouter()
const authChecking = ref(true)
const authenticated = ref(false)
const authUsername = ref('')
const authError = ref('')
const loginLoading = ref(false)
const loginForm = ref({ username: 'admin', password: 'admin' })
const appInitialized = ref(false)
const showOutboundMenu = ref(false)
const showUserMenu = ref(false)
const userMenuRef = ref(null)
const showImport = ref(false)
const showAccountModal = ref(false)
const currentOutbound = ref('proxy')
const startError = ref('')
const showCreateOutbound = ref(false)
const newOutboundName = ref('')
const creatingOutbound = ref(false)
const savingOutbound = ref(false)
const editingOutbound = ref(null)
const outboundForm = ref({ targetType: 'node', nodeKey: '', groupId: '', mode: 'leastping', probeInterval: '30s' })
const kernels = ref({})
// 出站弹窗里实时获取的数据（独立于 store 缓存）
const editGroups = ref([])
const editServers = ref([])
const editSubscriptions = ref([])
const accountSaving = ref(false)
const accountMsg = ref(null)
const passwordSaving = ref(false)
const passwordMsg = ref(null)
const accountForm = ref({ webUsername: '' })
const passwordForm = ref({
  oldPassword: '',
  newPassword: '',
  confirmPassword: '',
})

// 日志相关
const showLogs = ref(true)  // 默认展开
const logs = ref([])  // 现在存储对象数组 { time, message, level }
const logsStreaming = ref(true)
const logsAutoScroll = ref(true)
const logsContainer = ref(null)
const logsPanel = ref(null)
let logsStreamPromise = null
let logsEventSource = null
const logsPanelHeight = ref(null)  // 用户自定义高度

// 从 localStorage 恢复日志面板状态和高度
function restoreLogsPanelState() {
  const savedState = localStorage.getItem('showLogs')
  if (savedState !== null) {
    showLogs.value = savedState === 'true'
  }
  const saved = localStorage.getItem('logsPanelHeight')
  if (saved) {
    logsPanelHeight.value = parseInt(saved)
  }
}

// 拖拽调整日志面板高度
function startResize(e) {
  e.preventDefault()
  const panel = logsPanel.value
  if (!panel) return
  const startY = e.clientY
  const startH = panel.offsetHeight
  function onMove(ev) {
    const delta = startY - ev.clientY
    const newH = Math.min(Math.max(startH + delta, 80), window.innerHeight * 0.8)
    panel.style.height = newH + 'px'
    logsPanelHeight.value = newH
  }
  function onUp() {
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onUp)
    // 保存高度到 localStorage
    if (logsPanelHeight.value) {
      localStorage.setItem('logsPanelHeight', logsPanelHeight.value.toString())
    }
  }
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onUp)
}

// 当前选中出站的 target，用于全局状态条
const proxyTarget = computed(() => {
  const o = store.outbounds.find(ob => ob.name === currentOutbound.value)
  if (!o?.target?.targetType) return null
  return o.target
})

function getGroupName(id) {
  return store.groups.find(g => g.id === id)?.name || id
}

function getNodeName(ref) {
  if (!ref) return '未知'
  if (ref.type === 'server') {
    return store.servers[ref.index]?.name || `节点#${ref.index}`
  }
  if (ref.type === 'sub_server') {
    const subIdx = ref.sub ?? 0  // sub=0 时 omitempty 会省略，用 ?? 兜底
    const sub = store.subscriptions[subIdx]
    return sub?.servers?.[ref.index]?.name || `订阅节点#${subIdx}-${ref.index}`
  }
  return '未知'
}

const currentOutboundLabel = computed(() => {
  const o = store.outbounds.find(ob => ob.name === currentOutbound.value)
  if (!o) return currentOutbound.value.toUpperCase()
  const icon = o.target?.targetType === 'group' ? ' 📁' : o.target?.targetType === 'node' ? ' 🔗' : ''
  return currentOutbound.value.toUpperCase() + icon
})

provide('openImport', () => { showImport.value = true })
provide('currentOutbound', currentOutbound)

onMounted(() => {
  setUnauthorizedHandler(() => {
    closeLogsStream()
    authenticated.value = false
    authUsername.value = ''
    authChecking.value = false
    authError.value = '登录已失效，请重新登录'
  })
  checkAuth()
  document.addEventListener('click', handleGlobalClick)
})

onBeforeUnmount(() => {
  document.removeEventListener('click', handleGlobalClick)
})

function handleGlobalClick(event) {
  const el = userMenuRef.value
  if (!el) return
  if (!el.contains(event.target)) {
    showUserMenu.value = false
  }
}

async function selectOutbound(outbound) {
  currentOutbound.value = outbound?.name || 'proxy'
  showOutboundMenu.value = false
  if (router.currentRoute.value.path !== '/') {
    await router.push('/')
  }
}

// 监听 showLogs 变化，收缩时清除高度样式，并保存状态
watch(showLogs, (newVal) => {
  localStorage.setItem('showLogs', newVal.toString())
  if (!newVal && logsPanel.value) {
    // 收缩时，清除内联样式，让 CSS 接管
    logsPanel.value.style.height = ''
  } else if (newVal && logsPanel.value && logsPanelHeight.value) {
    // 展开时，恢复保存的高度
    logsPanel.value.style.height = logsPanelHeight.value + 'px'
  }
})

function onImportDone() {
  showImport.value = false
  store.fetchAll()
}

function resetAccountMessages() {
  accountMsg.value = null
  passwordMsg.value = null
}

function resetPasswordForm() {
  passwordForm.value = {
    oldPassword: '',
    newPassword: '',
    confirmPassword: '',
  }
}

function openAccountModal() {
  accountForm.value.webUsername = authUsername.value || 'admin'
  resetPasswordForm()
  resetAccountMessages()
  showUserMenu.value = false
  showAccountModal.value = true
}

function closeAccountModal() {
  showAccountModal.value = false
  resetPasswordForm()
  resetAccountMessages()
}

async function checkAuth() {
  authChecking.value = true
  try {
    const { data } = await api.me()
    authenticated.value = !!data.authenticated
    authUsername.value = data.username || ''
    authError.value = ''
    await initAuthenticatedApp()
  } catch {
    authenticated.value = false
    authUsername.value = ''
  } finally {
    authChecking.value = false
  }
}

async function handleLogin() {
  loginLoading.value = true
  authError.value = ''
  try {
    const { data } = await api.login(loginForm.value.username, loginForm.value.password)
    authenticated.value = true
    authUsername.value = data.username || loginForm.value.username
    await initAuthenticatedApp()
  } catch (e) {
    authError.value = e?.response?.data?.error || '登录失败'
  } finally {
    loginLoading.value = false
  }
}

async function handleLogout() {
  try {
    await api.logout()
  } catch {}
  closeLogsStream()
  authenticated.value = false
  authUsername.value = ''
  authError.value = ''
}

async function handleLogoutFromMenu() {
  showUserMenu.value = false
  await handleLogout()
}

async function initAuthenticatedApp() {
  if (!appInitialized.value) {
    restoreLogsPanelState()
    appInitialized.value = true
  }
  await store.fetchStatus()
  await store.fetchAll()
  api.getKernelStatus?.().then(r => { kernels.value = r.data.kernels || {} }).catch(() => {})
  await loadLogs()
  startLogsStream()
  await nextTick()
  if (logsPanel.value && showLogs.value) {
    if (!logsPanelHeight.value) {
      const defaultHeight = Math.floor(window.innerHeight * 0.4)
      logsPanel.value.style.height = defaultHeight + 'px'
      logsPanelHeight.value = defaultHeight
    } else {
      logsPanel.value.style.height = logsPanelHeight.value + 'px'
    }
  }
}

async function saveAccountProfile() {
  const username = String(accountForm.value.webUsername || '').trim()
  if (!username) {
    accountMsg.value = { error: true, text: '用户名不能为空' }
    return
  }
  accountSaving.value = true
  accountMsg.value = null
  try {
    const { data } = await api.getSetting()
    const setting = data.setting || {}
    await api.setSetting({ ...setting, webUsername: username })
    authUsername.value = username
    accountMsg.value = { error: false, text: '用户名已更新' }
  } catch (e) {
    accountMsg.value = { error: true, text: e?.response?.data?.error || '保存失败' }
  } finally {
    accountSaving.value = false
  }
}

async function changePassword() {
  passwordSaving.value = true
  passwordMsg.value = null
  if (!passwordForm.value.oldPassword || !passwordForm.value.newPassword || !passwordForm.value.confirmPassword) {
    passwordMsg.value = { error: true, text: '请完整填写密码字段' }
    passwordSaving.value = false
    return
  }
  try {
    await api.changePassword({ ...passwordForm.value })
    passwordMsg.value = { error: false, text: '密码已修改，请重新登录' }
    resetPasswordForm()
    setTimeout(async () => {
      closeAccountModal()
      await handleLogout()
    }, 800)
  } catch (e) {
    passwordMsg.value = { error: true, text: e?.response?.data?.error || '修改失败' }
  } finally {
    passwordSaving.value = false
  }
}

async function handleToggleProxy() {
  startError.value = ''
  try {
    await store.toggleProxy()
  } catch (e) {
    startError.value = e?.response?.data?.error || e?.message || '启动失败'
  }
}

async function openOutboundEdit(o) {
  // 先设置表单，弹窗立即打开
  editingOutbound.value = o
  const t = o.target || {}
  outboundForm.value = {
    targetType: t.targetType || 'node',
    nodeKey: t.nodeRef ? `${t.nodeRef.type}:${t.nodeRef.index}:${t.nodeRef.sub || 0}` : '',
    groupId: t.groupId || '',
    mode: t.mode || 'leastping',
    probeInterval: o.probeInterval || '30s',
  }
  // 实时从后端获取最新数据，直接写入弹窗专用变量
  const [gRes, sRes, subRes] = await Promise.all([
    api.getGroups(),
    api.getServers(),
    api.getSubscriptions(),
  ])
  editGroups.value = gRes.data.groups || []
  const ibRes = await api.getCustomInbounds()
  const inbounds = ibRes.data.inbounds || []
  const inboundIDSet = new Set(inbounds.map(ib => ib.id))
  editServers.value = (sRes.data.servers || []).filter(s => {
    if (typeof s.source !== 'string' || !s.source.startsWith('inbound:')) return true
    const rest = s.source.slice('inbound:'.length)
    const id = rest.split(':')[0]
    return inboundIDSet.has(id)
  })
  editSubscriptions.value = subRes.data.subscriptions || []
}

async function createOutbound() {
  if (creatingOutbound.value) return
  if (!newOutboundName.value.trim()) return
  creatingOutbound.value = true
  try {
    await api.createOutbound({ name: newOutboundName.value.trim() })
    showCreateOutbound.value = false
    newOutboundName.value = ''
    await store.fetchAll()
  } finally {
    creatingOutbound.value = false
  }
}

async function deleteOutbound(name) {
  await api.deleteOutbound(name)
  if (currentOutbound.value === name) currentOutbound.value = 'proxy'
  await store.fetchAll()
}

async function saveOutbound() {
  if (savingOutbound.value || !editingOutbound.value?.name) return
  savingOutbound.value = true
  try {
  const name = editingOutbound.value.name
  let target = {}
  if (outboundForm.value.targetType === 'node' && outboundForm.value.nodeKey) {
    const [type, index, sub] = outboundForm.value.nodeKey.split(':')
    target = { targetType: 'node', nodeRef: { type, index: parseInt(index), sub: parseInt(sub) } }
  } else if (outboundForm.value.targetType === 'group' && outboundForm.value.groupId) {
    target = { targetType: 'group', groupId: outboundForm.value.groupId, mode: outboundForm.value.mode }
  }
  await api.connectOutbound(name, target)
  editingOutbound.value = null
  await store.fetchAll()
  // 刷新代理状态，因为 connectOutbound 可能会触发 Restart
  await store.fetchStatus()
  } finally {
    savingOutbound.value = false
  }
}

async function disconnectOutbound() {
  await api.disconnectOutbound(editingOutbound.value.name)
  editingOutbound.value = null
  await store.fetchAll()
}


// 日志相关函数
async function loadLogs() {
  try {
    const res = await api.getLogs()
    // 将字符串数组转换为对象数组
    logs.value = (res.data.logs || []).map(log => {
      // 如果已经是对象，直接使用；否则解析字符串
      if (typeof log === 'object') {
        return log
      }
      // 从字符串中提取时间和消息
      const match = log.match(/^\[(\d{2}:\d{2}:\d{2})\]\s(.*)$/)
      if (match) {
        return { time: match[1], message: match[2], level: 'info' }
      }
      return { time: '', message: log, level: 'info' }
    })
    scrollLogsToBottom()
  } catch (e) {
    console.error('Failed to load logs:', e)
  }
}

function startLogsStream() {
  if (!authenticated.value || logsEventSource) return
  logsStreamPromise = new Promise((resolve, reject) => {
    const es = new EventSource('/api/logs/stream')
    logsEventSource = es
    es.onmessage = (e) => {
      try {
        const entry = JSON.parse(e.data)
        if (logsStreaming.value) {
          logs.value.push(entry)
          if (logs.value.length > 500) {
            logs.value = logs.value.slice(-500)
          }
          if (logsAutoScroll.value) {
            scrollLogsToBottom()
          }
        }
      } catch {}
    }
    es.onerror = () => {
      if (es.readyState === EventSource.CLOSED) return
      if (logsEventSource === es) {
        logsEventSource = null
        logsStreamPromise = null
      }
      es.close()
      reject(new Error('日志连接中断'))
    }
  }).catch(() => {
    // 连接断开，3秒后重试
    setTimeout(() => {
      if (authenticated.value) startLogsStream()
    }, 3000)
  })
}

function closeLogsStream() {
  if (logsEventSource) {
    logsEventSource.close()
    logsEventSource = null
  }
  logsStreamPromise = null
}

function toggleLogsStream() {
  logsStreaming.value = !logsStreaming.value
}

function toggleAutoScroll() {
  logsAutoScroll.value = !logsAutoScroll.value
  if (logsAutoScroll.value) {
    scrollLogsToBottom()
  }
}

function clearLogs() {
  logs.value = []
}

function scrollLogsToBottom() {
  if (logsContainer.value) {
    setTimeout(() => {
      logsContainer.value.scrollTop = logsContainer.value.scrollHeight
    }, 0)
  }
}
</script>

<style>
* { box-sizing: border-box; margin: 0; padding: 0; }
html, body { height: 100%; }
body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', 'PingFang SC', 'Microsoft YaHei', sans-serif;
  background: #f0f2f5;
  color: #262626;
  font-size: 14px;
}
#app { display: flex; flex-direction: column; height: 100vh; }
.auth-screen {
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background:
    radial-gradient(circle at top left, rgba(50,115,220,.18), transparent 30%),
    radial-gradient(circle at bottom right, rgba(72,199,116,.14), transparent 28%),
    #f3f6fb;
}
.auth-card {
  width: 100%;
  max-width: 360px;
  background: rgba(255,255,255,.92);
  border: 1px solid rgba(219,219,219,.9);
  border-radius: 14px;
  box-shadow: 0 18px 50px rgba(0,0,0,.08);
  padding: 28px;
}
.auth-title {
  font-size: 28px;
  font-weight: 800;
  color: #3273dc;
  margin-bottom: 8px;
}
.auth-subtitle {
  color: #6b7280;
  font-size: 13px;
  margin-bottom: 16px;
}
.auth-input {
  margin-bottom: 12px;
}
.auth-submit {
  width: 100%;
  justify-content: center;
  margin-top: 4px;
}
.auth-error {
  margin-top: 12px;
  color: #f14668;
  font-size: 13px;
}

/* ===== Navbar ===== */
.navbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  background: #f5f5f5;
  border-bottom: 1px solid #dbdbdb;
  padding: 0 16px;
  height: 52px;
  position: sticky;
  top: 0;
  z-index: 50;
  box-shadow: 0 1px 3px rgba(0,0,0,.08);
}
.navbar-left { display: flex; align-items: center; gap: 10px; }
.navbar-right { display: flex; align-items: center; gap: 4px; }

.brand { display: flex; align-items: center; gap: 6px; margin-right: 4px; }
.brand-icon { font-size: 20px; }
.brand-name { font-size: 16px; font-weight: 700; color: #3273dc; letter-spacing: -.3px; }

/* 状态标签 */
.status-tag {
  display: inline-flex; align-items: center;
  padding: 3px 10px;
  border-radius: 4px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  user-select: none;
  transition: opacity .15s;
}
.status-tag:hover { opacity: .8; }
.status-running { background: #48c774; color: #fff; }
.status-stopped { background: #f14668; color: #fff; }

.start-error {
  display: inline-flex;
  align-items: center;
  padding: 3px 10px;
  border-radius: 4px;
  font-size: 12px;
  background: #fff3cd;
  color: #856404;
  border: 1px solid #ffc107;
  max-width: 300px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* 出站下拉 */
.outbound-dropdown { position: relative; }
.outbound-tag {
  display: inline-flex; align-items: center; gap: 4px;
  padding: 3px 10px;
  border-radius: 4px;
  background: #3273dc;
  color: #fff;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  user-select: none;
}
.caret { font-size: 10px; }
.dropdown-menu {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  background: #fff;
  border: 1px solid #dbdbdb;
  border-radius: 6px;
  box-shadow: 0 4px 16px rgba(0,0,0,.12);
  min-width: 260px;
  z-index: 100;
  overflow: hidden;
}
.dropdown-item {
  display: flex; align-items: center; justify-content: space-between;
  padding: 8px 14px;
  cursor: pointer;
  font-size: 13px;
  transition: background .1s;
  white-space: nowrap;
  gap: 6px;
}
.dropdown-item:hover { background: #f5f5f5; }
.dropdown-item.active { color: #3273dc; font-weight: 600; }
.dropdown-divider { height: 1px; background: #dbdbdb; margin: 4px 0; }
.outbound-target-badge { font-size: 12px; margin-left: 4px; }
.dd-edit-btn, .dd-del-btn {
  padding: 1px 5px;
  border-radius: 3px;
  font-size: 12px;
  cursor: pointer;
  color: #8c8c8c;
  margin-left: 4px;
  flex-shrink: 0;
}
.dd-edit-btn:hover { background: #e6f0ff; color: #1677ff; }
.dd-del-btn:hover { background: #fff1f0; color: #ff4d4f; }

/* Navbar 右侧按钮 */
.nav-btn {
  display: flex; align-items: center; gap: 5px;
  padding: 6px 12px;
  border-radius: 4px;
  color: #4a4a4a;
  text-decoration: none;
  font-size: 13px;
  transition: background .1s;
}
.nav-btn:hover { background: #f5f5f5; }
.nav-btn-active { color: #3273dc; background: #ebf3ff; }
.user-menu { position: relative; margin-left: 8px; }
.user-menu-trigger {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border: 1px solid #dbdbdb;
  border-radius: 999px;
  background: #fff;
  cursor: pointer;
  transition: background .15s, border-color .15s;
}
.user-menu-trigger:hover { background: #f5f5f5; border-color: #bfbfbf; }
.user-dropdown-menu {
  position: absolute;
  top: calc(100% + 2px);
  right: 0;
  min-width: 148px;
  background: #fff;
  border: 1px solid #dbdbdb;
  border-radius: 8px;
  box-shadow: 0 8px 24px rgba(0,0,0,.12);
  overflow: hidden;
  z-index: 120;
}
.user-dropdown-item {
  display: block;
  width: 100%;
  padding: 10px 12px;
  border: 0;
  background: transparent;
  text-align: left;
  font-size: 13px;
  color: #363636;
  cursor: pointer;
}
.user-dropdown-item:hover { background: #f5f5f5; }
.user-dropdown-danger { color: #f14668; }
.auth-user {
  font-size: 12px;
  color: #6b7280;
  margin-left: 0;
}

/* ===== Main ===== */
.main-content { flex: 1; overflow-y: auto; display: flex; flex-direction: column; }

/* 全局出站状态条 */
.global-status-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 6px 20px;
  background: #e6f4ff;
  border-bottom: 1px solid #bae0ff;
  font-size: 12px;
  color: #1677ff;
  flex-shrink: 0;
}
.gsb-empty { background: #fffbe6; border-bottom-color: #ffe58f; color: #8c6d00; }
.gsb-icon { font-size: 13px; }
.gsb-sep { color: #bfbfbf; }
.gsb-btn {
  margin-left: auto;
  background: none;
  border: 1px solid currentColor;
  border-radius: 4px;
  padding: 2px 10px;
  font-size: 11px;
  cursor: pointer;
  color: inherit;
  transition: opacity .15s;
}
.gsb-btn:hover { opacity: .7; }

/* ===== 通用组件样式 ===== */
.btn {
  display: inline-flex; align-items: center; gap: 4px;
  padding: 6px 14px;
  border: 1px solid transparent;
  border-radius: 4px;
  font-size: 13px;
  cursor: pointer;
  transition: all .15s;
  white-space: nowrap;
}
.btn-primary { background: #3273dc; color: #fff; border-color: #3273dc; }
.btn-primary:hover { background: #2366d1; }
.btn-success { background: #48c774; color: #fff; border-color: #48c774; }
.btn-success:hover { background: #3ec46d; }
.btn-danger { background: #f14668; color: #fff; border-color: #f14668; }
.btn-danger:hover { background: #ef2e55; }
.btn-light { background: #fff; color: #363636; border-color: #dbdbdb; }
.btn-light:hover { background: #f5f5f5; border-color: #b5b5b5; }
.btn-sm { padding: 3px 9px; font-size: 12px; }

.tag {
  display: inline-block;
  padding: 2px 7px;
  border-radius: 3px;
  font-size: 11px;
  font-weight: 600;
}
.tag-vmess  { background: #ebf3ff; color: #3273dc; }
.tag-vless  { background: #effaf5; color: #257953; }
.tag-ss     { background: #fff5eb; color: #c05621; }
.tag-ssr    { background: #fff5eb; color: #c05621; }
.tag-trojan { background: #f5f0ff; color: #7c3aed; }
.tag-hysteria2, .tag-hy2, .tag-hysteria { background: #e8f8ff; color: #0077b6; }
.tag-tuic   { background: #fef9e7; color: #b7791f; }
.tag-wireguard { background: #f0fff4; color: #276749; }
.tag-socks5, .tag-socks, .tag-socks4 { background: #faf0ff; color: #6b46c1; }
.tag-http, .tag-https, .tag-naive { background: #f7fafc; color: #4a5568; }

.input {
  display: block;
  width: 100%;
  padding: 7px 10px;
  border: 1px solid #dbdbdb;
  border-radius: 4px;
  font-size: 13px;
  color: #363636;
  background: #fff;
  outline: none;
  transition: border-color .15s;
}
.input:focus { border-color: #3273dc; box-shadow: 0 0 0 2px rgba(50,115,220,.15); }

.modal-overlay {
  position: fixed; inset: 0;
  background: rgba(10,10,10,.5);
  display: flex; align-items: center; justify-content: center;
  z-index: 200;
}
.modal-box {
  background: #fff;
  border-radius: 8px;
  box-shadow: 0 8px 32px rgba(0,0,0,.18);
  width: 520px;
  max-width: 95vw;
  max-height: 90vh;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.modal-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid #dbdbdb;
  flex-shrink: 0;
  position: sticky;
  top: 0;
  z-index: 1;
  background: #fff;
  box-shadow: 0 1px 0 rgba(0,0,0,.04);
}
.modal-title { font-size: 16px; font-weight: 600; }
.modal-close { cursor: pointer; font-size: 18px; color: #7a7a7a; line-height: 1; }
.modal-close:hover { color: #363636; }
.modal-body {
  padding: 20px;
  overflow-y: auto;
  min-height: 0;
}
.modal-footer {
  display: flex; justify-content: flex-end; gap: 8px;
  padding: 14px 20px;
  border-top: 1px solid #dbdbdb;
  flex-shrink: 0;
  position: sticky;
  bottom: 0;
  z-index: 1;
  background: #fff;
  box-shadow: 0 -1px 0 rgba(0,0,0,.04);
}
.account-divider {
  height: 1px;
  background: #f0f0f0;
  margin: 18px 0;
}
.account-msg {
  margin-top: 10px;
  padding: 10px 12px;
  border-radius: 6px;
  font-size: 12px;
}
.account-msg-success {
  background: #f6ffed;
  border: 1px solid #b7eb8f;
  color: #389e0d;
}
.account-msg-error {
  background: #fff2f0;
  border: 1px solid #ffccc7;
  color: #cf1322;
}

.form-group { margin-bottom: 14px; }
.form-label { display: block; font-size: 12px; font-weight: 600; color: #4a4a4a; margin-bottom: 5px; }

/* 延迟颜色 */
.lat-good  { color: #48c774; font-weight: 600; }
.lat-ok    { color: #ffdd57; font-weight: 600; }
.lat-bad   { color: #f14668; font-weight: 600; }
.lat-none  { color: #b5b5b5; }

/* ===== 日志面板 ===== */
.logs-panel {
  display: flex;
  flex-direction: column;
  background: #fff;
  border-top: 1px solid #dbdbdb;
  flex-shrink: 0;
  height: 32px;
  overflow: hidden;
  transition: height 0.3s ease;
}
.logs-panel-expanded {
  height: auto;
  min-height: 80px;
  max-height: 80vh;
  overflow: hidden;
}
.logs-resize-handle {
  height: 5px;
  background: #e8e8e8;
  cursor: ns-resize;
  flex-shrink: 0;
}
.logs-resize-handle:hover, .logs-resize-handle:active {
  background: #3273dc;
}
.logs-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  background: #f5f5f5;
  border-bottom: 1px solid #dbdbdb;
  cursor: pointer;
  user-select: none;
  flex-shrink: 0;
  height: 32px;
  min-height: 32px;
}
.logs-title {
  font-size: 13px;
  font-weight: 600;
  color: #363636;
}
.logs-toggle {
  font-size: 12px;
  color: #8c8c8c;
}
.logs-content {
  display: flex;
  flex-direction: column;
  flex: 1;
  overflow: hidden;
  min-height: 0;
}
.logs-list {
  flex: 1;
  overflow-y: auto;
  padding: 8px 12px;
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', monospace;
  font-size: 11px;
  line-height: 1.5;
  background: #1a1a1a;
  color: #e0e0e0;
  min-height: 0;
}
.log-line {
  color: #e0e0e0;
  white-space: pre-wrap;
  word-break: break-all;
  margin-bottom: 2px;
  display: flex;
  gap: 8px;
}
.log-time {
  color: #888;
  flex-shrink: 0;
  min-width: 12ch;
}
.log-message {
  flex: 1;
}
.log-info .log-message {
  color: #e0e0e0;
}
.log-success .log-message {
  color: #52c41a;
}
.log-warn .log-message {
  color: #faad14;
}
.log-error .log-message {
  color: #ff4d4f;
}
.log-debug .log-message {
  color: #1890ff;
}
.log-empty {
  color: #666;
  text-align: center;
  padding: 20px;
}
.logs-actions {
  display: flex;
  gap: 6px;
  padding: 6px 12px;
  border-top: 1px solid #f0f0f0;
  background: #fafafa;
  flex-shrink: 0;
}

@media (max-width: 900px) {
  #app { height: 100dvh; }
  .navbar {
    height: auto;
    min-height: 52px;
    padding: 8px 10px;
    gap: 8px;
    align-items: flex-start;
    flex-direction: column;
  }
  .navbar-left, .navbar-right {
    width: 100%;
    flex-wrap: wrap;
    row-gap: 6px;
  }
  .navbar-right { justify-content: flex-start; }
  .global-status-bar {
    padding: 8px 10px;
    gap: 6px;
    flex-wrap: wrap;
  }
  .gsb-btn { margin-left: 0; }
  .main-content { min-height: 0; }
  .modal-box {
    width: calc(100vw - 20px) !important;
    max-width: calc(100vw - 20px);
    max-height: 85dvh;
  }
  .modal-body { padding: 14px; }
  .modal-header, .modal-footer { padding: 12px 14px; }
  .logs-header { padding: 0 10px; }
  .logs-actions {
    padding: 6px 10px;
    flex-wrap: wrap;
  }
}
</style>

