<template>
  <div class="settings-page">

    <!-- 内核文件状态栏 -->
    <div class="data-files-bar">
      <span class="data-label">内核文件：</span>
      <span v-for="k in kernelFiles" :key="k.id" :class="['data-badge', k.installed ? 'data-ok' : 'data-missing']" :title="kernelBarTitle(k)">
        {{ k.label }} {{ kernelBarText(k) }}
      </span>
      <button class="action-btn" style="margin-left:auto" @click="showKernelModal = true">⬇ 下载/更新内核</button>
    </div>

    <!-- 常规设置 -->
    <div class="settings-block">
      <div class="block-header">
        <span class="block-title">日志</span>
      </div>
      <div class="field-row">
        <label class="field-label">日志级别</label>
        <select class="input field-input" v-model="form.logLevel">
          <option value="debug">Debug</option>
          <option value="info">Info</option>
          <option value="warn">Warn</option>
          <option value="error">Error</option>
        </select>
      </div>
      <div class="field-row">
        <label class="field-label">最大日志行数</label>
        <input type="number" class="input field-input" v-model.number="form.maxLogLines" />
      </div>
      <div class="field-row">
        <label class="field-label">最大日志文件（MB）</label>
        <input type="number" class="input field-input" v-model.number="form.maxLogFileSizeMB" step="0.1" />
      </div>
    </div>

    <div class="settings-block">
      <div class="block-header">
        <span class="block-title">代理</span>
      </div>
      <div class="field-row">
        <label class="field-label">透明代理模式</label>
        <select class="input field-input" v-model="form.transparentMode">
          <option value="close">关闭</option>
          <option value="open">开启</option>
        </select>
      </div>
      <div class="field-row">
        <label class="field-label">局域网共享</label>
        <label class="toggle-wrap">
          <input type="checkbox" v-model="form.lanSharingEnabled" />
          <span>{{ form.lanSharingEnabled ? '已开启，监听 0.0.0.0' : '已关闭，仅监听 127.0.0.1' }}</span>
        </label>
      </div>
      <div class="field-row field-row-tip">
        <label class="field-label"></label>
        <div class="field-tip">
          开启后，局域网设备可通过宿主机地址访问代理端口。建议同时配置 SOCKS5 / HTTP 认证，避免未授权使用。
        </div>
      </div>
      <div class="field-row field-row-tip">
        <label class="field-label">当前监听</label>
        <div class="listen-summary">
          <div class="listen-item">
            <span class="listen-label">SOCKS5</span>
            <code class="listen-code">{{ builtinListenHost }}:{{ form.socks5Port }}</code>
          </div>
          <div class="listen-item">
            <span class="listen-label">HTTP</span>
            <code class="listen-code">{{ builtinListenHost }}:{{ form.httpPort }}</code>
          </div>
        </div>
      </div>
    </div>

    <div class="settings-block">
      <div class="block-header">
        <span class="block-title">管理端认证</span>
      </div>
      <div class="field-row">
        <label class="field-label">用户名</label>
        <input class="input field-input" v-model="form.webUsername" placeholder="admin" />
      </div>
    </div>

    <div class="settings-block">
      <div class="block-header">
        <span class="block-title">修改密码</span>
      </div>
      <div class="field-row">
        <label class="field-label">旧密码</label>
        <input type="password" class="input field-input" v-model="passwordForm.oldPassword" placeholder="当前密码" />
      </div>
      <div class="field-row">
        <label class="field-label">新密码</label>
        <input type="password" class="input field-input" v-model="passwordForm.newPassword" placeholder="新密码" />
      </div>
      <div class="field-row">
        <label class="field-label">确认新密码</label>
        <input type="password" class="input field-input" v-model="passwordForm.confirmPassword" placeholder="再次输入新密码" />
      </div>
      <div class="field-row">
        <label class="field-label"></label>
        <div class="password-actions">
          <button class="action-btn" :disabled="passwordSaving" @click="changePassword">
            {{ passwordSaving ? '修改中...' : '修改密码' }}
          </button>
          <span v-if="passwordMsg" :class="['save-msg', passwordMsg.error ? 'msg-error' : 'msg-success']">
            {{ passwordMsg.text }}
          </span>
        </div>
      </div>
    </div>

    <div class="settings-block">
      <div class="block-header">
        <span class="block-title">订阅更新</span>
      </div>
      <div class="field-row">
        <label class="field-label">自动更新</label>
        <select class="input field-input" v-model="form.subscriptionAutoUpdateMode">
          <option value="off">关闭</option>
          <option value="on">开启</option>
        </select>
      </div>
      <div class="field-row" v-if="form.subscriptionAutoUpdateMode === 'on'">
        <label class="field-label">更新间隔（小时）</label>
        <input type="number" class="input field-input" v-model.number="form.subscriptionAutoUpdateIntervalHour" />
      </div>
    </div>

    <!-- 保存按钮 -->
    <div class="save-bar">
      <button class="action-btn action-btn-primary" :disabled="saving" @click="saveSettings">
        {{ saving ? '保存中...' : '保存设置' }}
      </button>
      <span v-if="saveMsg" :class="['save-msg', saveMsg.error ? 'msg-error' : 'msg-success']">
        {{ saveMsg.text }}
      </span>
    </div>

  </div>

  <!-- 内核下载弹窗 -->
  <div class="modal-overlay" v-if="showKernelModal" @click.self="showKernelModal = false">
    <div class="modal-box" style="width:560px">
      <div class="modal-header">
        <span class="modal-title">内核管理</span>
        <span class="modal-close" @click="showKernelModal = false">✕</span>
      </div>
      <div class="modal-body">
        <div v-for="k in kernelFiles" :key="k.id" class="data-file-row">
          <div class="data-file-info">
            <span class="data-file-name">{{ k.label }}</span>
            <span :class="['data-badge', k.installed ? 'data-ok' : 'data-missing']">
              {{ kernelBadgeText(k) }}
            </span>
            <span class="data-file-desc">{{ k.desc }}</span>
            <span v-if="k.installed" class="kernel-version-text">
              当前：{{ k.localVersion || '未知' }}<template v-if="k.latestVersion"> · 最新：{{ k.latestVersion }}</template>
            </span>
          </div>
          <div>
            <template v-if="kernelDownloading[k.id]">
              <div class="dl-progress">
                <div class="dl-bar"><div class="dl-fill" :style="{ width: kernelDownloading[k.id].percent + '%' }"></div></div>
                <span class="dl-msg">{{ kernelDownloading[k.id].message }}</span>
              </div>
            </template>
            <button v-else :class="['btn','btn-sm', kernelActionClass(k)]"
              :disabled="k.installed && !k.hasUpdate"
              @click="downloadKernel(k.id)">
              {{ kernelActionText(k) }}
            </button>
          </div>
        </div>

        <div style="margin-top:12px;padding:10px 12px;background:#f0f7ff;border-radius:6px;font-size:12px;border:1px solid #bae0ff">
          <div style="font-weight:600;color:#1677ff;margin-bottom:6px">🚀 GitHub 加速镜像（可选）</div>
          <div style="display:flex;gap:8px;align-items:center;margin-bottom:6px">
            <input class="input" v-model="form.githubMirror" placeholder="如 https://ghfast.top" style="flex:1;font-size:12px" />
          </div>
          <div style="color:#8c8c8c;line-height:1.6">
            常用镜像：
            <span v-for="m in mirrorPresets" :key="m" class="mirror-preset" @click="form.githubMirror = m">{{ m }}</span>
            <span class="mirror-preset" style="color:#ff4d4f" @click="form.githubMirror = ''">清除</span>
          </div>
        </div>
      </div>
      <div class="modal-footer">
        <button class="btn btn-light" @click="showKernelModal = false">关闭</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted, computed } from 'vue'
import { api } from '../api'

const saving = ref(false)
const saveMsg = ref(null)
const passwordSaving = ref(false)
const passwordMsg = ref(null)
const showKernelModal = ref(false)

const form = reactive({
  logLevel: 'info',
  subscriptionAutoUpdateMode: 'off',
  subscriptionAutoUpdateIntervalHour: 12,
  transparentMode: 'close',
  lanSharingEnabled: false,
  socks5Port: 20260,
  httpPort: 20261,
  maxLogLines: 500,
  maxLogFileSizeMB: 2,
  githubMirror: '',
  webUsername: 'admin',
})
const passwordForm = reactive({
  oldPassword: '',
  newPassword: '',
  confirmPassword: '',
})

const kernelFiles = ref([
  { id: 'singbox', label: 'sing-box', desc: '主力内核，支持多协议', installed: false, localVersion: '', latestVersion: '', hasUpdate: false, checkError: '' },
  { id: 'xray',    label: 'Xray',     desc: 'XTLS/Xray-core',       installed: false, localVersion: '', latestVersion: '', hasUpdate: false, checkError: '' },
  { id: 'v2ray',   label: 'V2Ray',    desc: 'v2fly/v2ray-core',     installed: false, localVersion: '', latestVersion: '', hasUpdate: false, checkError: '' },
])
const kernelDownloading = reactive({})
const mirrorPresets = ['https://ghfast.top', 'https://mirror.ghproxy.com', 'https://gh-proxy.com']
const builtinListenHost = computed(() => form.lanSharingEnabled ? '0.0.0.0' : '127.0.0.1')
let fullSetting = {}

onMounted(async () => {
  try {
    const { data } = await api.getSetting()
    const s = data.setting
    fullSetting = { ...s }
    Object.assign(form, {
      logLevel: s.logLevel || 'info',
      subscriptionAutoUpdateMode: s.subscriptionAutoUpdateMode || 'off',
      subscriptionAutoUpdateIntervalHour: s.subscriptionAutoUpdateIntervalHour || 12,
      transparentMode: s.transparentMode || 'close',
      lanSharingEnabled: !!s.lanSharingEnabled,
      socks5Port: s.socks5Port || 20260,
      httpPort: s.httpPort || 20261,
      maxLogLines: s.maxLogLines || 500,
      maxLogFileSizeMB: (s.maxLogFileSize || 2097152) / (1024 * 1024),
      githubMirror: s.githubMirror || '',
      webUsername: s.webUsername || 'admin',
    })
  } catch {}
  await refreshKernelStatus()
})

async function downloadKernel(id) {
  const item = kernelFiles.value.find(k => k.id === id)
  if (item?.installed && !item.hasUpdate) return

  // 先保存镜像设置
  try {
    await api.setSetting({ ...fullSetting, githubMirror: form.githubMirror })
    fullSetting.githubMirror = form.githubMirror
  } catch {}

  kernelDownloading[id] = { percent: 0, message: '准备中…' }
  try {
    await api.downloadKernel(id, (p) => { kernelDownloading[id] = { percent: p.percent, message: p.message } })
    await refreshKernelStatus()
  } catch (e) {
    kernelDownloading[id] = { percent: 0, message: '失败: ' + (e?.message || '未知错误') }
    setTimeout(() => { delete kernelDownloading[id] }, 4000)
    return
  }
  delete kernelDownloading[id]
}

async function refreshKernelStatus() {
  try {
    const { data } = await api.getKernelStatus()
    const kernels = data.kernels || {}
    const meta = data.kernelMeta || {}
    kernelFiles.value.forEach(k => {
      const info = meta[k.id] || {}
      k.installed = !!kernels[k.id]
      k.localVersion = info.localVersion || ''
      k.latestVersion = info.latestVersion || ''
      k.hasUpdate = !!info.hasUpdate
      k.checkError = info.checkError || ''
    })
  } catch {}
}

function kernelBadgeText(k) {
  if (!k.installed) return '未安装'
  if (k.hasUpdate) return '可更新'
  if (k.localVersion) return `已安装 ${k.localVersion}`
  return '已安装'
}

function kernelBarText(k) {
  if (!k.installed) return '未安装'
  if (k.hasUpdate) return `${k.localVersion || '已安装'} -> ${k.latestVersion || '可更新'}`
  return k.localVersion || '已安装'
}

function kernelBarTitle(k) {
  if (!k.installed) return `${k.label} 未安装`
  if (k.latestVersion) return `${k.label}\n当前版本: ${k.localVersion || '未知'}\n最新版本: ${k.latestVersion}`
  return `${k.label}\n当前版本: ${k.localVersion || '未知'}`
}

function kernelActionText(k) {
  if (!k.installed) return '下载'
  if (k.hasUpdate) return '更新'
  return '已最新'
}

function kernelActionClass(k) {
  return k.installed ? 'btn-light' : 'btn-primary'
}

async function saveSettings() {
  saving.value = true
  saveMsg.value = null
  if (!String(form.webUsername || '').trim()) {
    saveMsg.value = { error: true, text: '用户名不能为空' }
    saving.value = false
    return
  }
  try {
    await api.setSetting({
      ...fullSetting,
      logLevel: form.logLevel,
      subscriptionAutoUpdateMode: form.subscriptionAutoUpdateMode,
      subscriptionAutoUpdateIntervalHour: form.subscriptionAutoUpdateIntervalHour,
      transparentMode: form.transparentMode,
      lanSharingEnabled: form.lanSharingEnabled,
      maxLogLines: form.maxLogLines,
      maxLogFileSize: Math.round(form.maxLogFileSizeMB * 1024 * 1024),
      githubMirror: form.githubMirror,
      webUsername: form.webUsername.trim(),
    })
    fullSetting = {
      ...fullSetting,
      logLevel: form.logLevel,
      subscriptionAutoUpdateMode: form.subscriptionAutoUpdateMode,
      subscriptionAutoUpdateIntervalHour: form.subscriptionAutoUpdateIntervalHour,
      transparentMode: form.transparentMode,
      lanSharingEnabled: form.lanSharingEnabled,
      maxLogLines: form.maxLogLines,
      maxLogFileSize: Math.round(form.maxLogFileSizeMB * 1024 * 1024),
      githubMirror: form.githubMirror,
      webUsername: form.webUsername.trim(),
    }
    saveMsg.value = { error: false, text: '保存成功' }
    setTimeout(() => { saveMsg.value = null }, 3000)
  } catch (e) {
    saveMsg.value = { error: true, text: e?.response?.data?.error || '保存失败' }
  } finally {
    saving.value = false
  }
}

async function changePassword() {
  passwordSaving.value = true
  passwordMsg.value = null
  if (!passwordForm.oldPassword || !passwordForm.newPassword || !passwordForm.confirmPassword) {
    passwordMsg.value = { error: true, text: '请完整填写密码字段' }
    passwordSaving.value = false
    return
  }
  try {
    await api.changePassword({ ...passwordForm })
    passwordMsg.value = { error: false, text: '密码已修改，请重新登录' }
    passwordForm.oldPassword = ''
    passwordForm.newPassword = ''
    passwordForm.confirmPassword = ''
    setTimeout(() => { window.location.reload() }, 800)
  } catch (e) {
    passwordMsg.value = { error: true, text: e?.response?.data?.error || '修改失败' }
  } finally {
    passwordSaving.value = false
  }
}
</script>

<style scoped>
.settings-page {
  padding: 20px 24px;
  max-width: 800px;
  margin: 0 auto;
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

/* 内核状态栏 - 完全复用 RulesView 的 data-files-bar */
.data-files-bar {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 12px;
  background: #fafafa;
  border: 1px solid #e8e8e8;
  border-radius: 6px;
  font-size: 12px;
}
.data-label { color: #8c8c8c; }
.data-badge {
  padding: 2px 8px; border-radius: 10px; font-size: 11px; font-weight: 600;
}
.data-ok { background: #f6ffed; color: #52c41a; border: 1px solid #b7eb8f; }
.data-missing { background: #fff2f0; color: #ff4d4f; border: 1px solid #ffccc7; }

/* 设置区块 */
.settings-block {
  background: #fff;
  border: 1px solid #e8e8e8;
  border-radius: 8px;
  overflow: hidden;
}

.block-header {
  padding: 10px 16px;
  background: #fafafa;
  border-bottom: 1px solid #f0f0f0;
}

.block-title {
  font-size: 13px;
  font-weight: 600;
  color: #595959;
}

.field-row {
  display: flex; align-items: center;
  padding: 11px 16px;
  border-bottom: 1px solid #f5f5f5;
  gap: 16px;
}
.field-row:last-child { border-bottom: none; }

.field-label {
  font-size: 13px; color: #595959;
  width: 160px; flex-shrink: 0;
}

.field-input { flex: 1; max-width: 300px; }
.toggle-wrap {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 1;
  font-size: 13px;
  color: #262626;
}
.field-row-tip {
  align-items: flex-start;
}
.field-tip {
  flex: 1;
  max-width: 520px;
  font-size: 12px;
  line-height: 1.6;
  color: #8c8c8c;
}
.listen-summary {
  display: flex;
  flex-direction: column;
  gap: 8px;
  flex: 1;
}
.listen-item {
  display: flex;
  align-items: center;
  gap: 10px;
}
.listen-label {
  width: 56px;
  font-size: 12px;
  color: #8c8c8c;
}
.listen-code {
  display: inline-block;
  padding: 3px 8px;
  border-radius: 6px;
  background: #f5f5f5;
  border: 1px solid #e8e8e8;
  color: #262626;
  font-size: 12px;
}
.password-actions { display: flex; align-items: center; gap: 12px; flex: 1; }

.preset-wrap { display: flex; flex-wrap: wrap; gap: 6px; flex: 1; }

.mirror-preset {
  display: inline-block;
  padding: 1px 8px; border-radius: 10px; cursor: pointer;
  background: #e6f4ff; color: #1677ff; font-size: 11px; font-family: monospace;
}
.mirror-preset:hover { background: #bae0ff; }

/* 保存栏 */
.save-bar {
  display: flex; align-items: center; gap: 12px;
  padding: 4px 0;
}

.save-msg { font-size: 13px; }
.msg-success { color: #52c41a; }
.msg-error { color: #ff4d4f; }

/* 弹窗内数据文件行 - 复用 RulesView 样式 */
.data-file-row {
  display: flex; align-items: center; justify-content: space-between;
  padding: 12px 0; border-bottom: 1px solid #f0f0f0;
}
.data-file-row:last-child { border-bottom: none; }
.data-file-info { display: flex; align-items: center; gap: 8px; flex: 1; }
.data-file-name { font-size: 13px; font-weight: 600; font-family: monospace; }
.data-file-desc { font-size: 12px; color: #8c8c8c; }
.kernel-version-text { font-size: 12px; color: #8c8c8c; }

.dl-progress { display: flex; flex-direction: column; gap: 4px; min-width: 120px; }
.dl-bar { height: 4px; background: #f0f0f0; border-radius: 2px; overflow: hidden; }
.dl-fill { height: 100%; background: #1677ff; border-radius: 2px; transition: width .3s; }
.dl-msg { font-size: 11px; color: #8c8c8c; }

.action-btn {
  display: inline-flex; align-items: center; gap: 4px;
  padding: 5px 12px; border: 1px solid #d9d9d9; border-radius: 6px;
  background: #fff; color: #595959; font-size: 12px; cursor: pointer; transition: all .15s;
}
.action-btn:hover { border-color: #1677ff; color: #1677ff; background: #f5f8ff; }
.action-btn-primary { background: #1677ff; color: #fff; border-color: #1677ff; }
.action-btn-primary:hover { background: #0958d9; border-color: #0958d9; color: #fff; }
.action-btn:disabled { opacity: .6; cursor: not-allowed; }

.input {
  display: block; width: 100%; padding: 6px 10px;
  border: 1px solid #d9d9d9; border-radius: 4px;
  font-size: 13px; color: #262626; background: #fff;
  outline: none; transition: border-color .15s;
}
.input:focus { border-color: #1677ff; box-shadow: 0 0 0 2px rgba(22,119,255,.15); }

.btn { display: inline-flex; align-items: center; gap: 4px; padding: 6px 14px; border: 1px solid transparent; border-radius: 4px; font-size: 13px; cursor: pointer; transition: all .15s; white-space: nowrap; }
.btn-primary { background: #1677ff; color: #fff; border-color: #1677ff; }
.btn-primary:hover { background: #0958d9; }
.btn-light { background: #fff; color: #363636; border-color: #dbdbdb; }
.btn-light:hover { background: #f5f5f5; }
.btn-sm { padding: 3px 9px; font-size: 12px; }
</style>
