<template>
  <div class="modal-overlay" @click.self="$emit('close')">
    <div class="modal-box setting-modal-box">
      <div class="modal-header">
        <span class="modal-title">设置</span>
        <span class="modal-close" @click="$emit('close')"></span>
      </div>
      <div class="setting-layout">
        <div class="setting-nav">
          <div v-for="tab in tabs" :key="tab.id"
            :class="['nav-item', activeTab === tab.id ? 'nav-active' : '']"
            @click="activeTab = tab.id">
            <span class="nav-icon">{{ tab.icon }}</span>
            <span>{{ tab.label }}</span>
          </div>
        </div>
        <div class="setting-content">
          <div v-if="activeTab === 'general'">
            <div class="content-title">常规</div>
            <div class="field-group">
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
              <div class="field-row">
                <label class="field-label">透明代理模式</label>
                <select class="input field-input" v-model="form.transparentMode">
                  <option value="close">关闭</option>
                  <option value="open">开启</option>
                </select>
              </div>
            </div>
            <div class="content-title" style="margin-top:24px">订阅更新</div>
            <div class="field-group">
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
          </div>
          <div v-if="activeTab === 'download'">
            <div class="content-title">GitHub 加速镜像</div>
            <div class="field-group">
              <div class="field-row">
                <label class="field-label">镜像地址</label>
                <input type="text" class="input field-input" v-model="form.githubMirror" placeholder="如 https://ghfast.top" />
              </div>
              <div class="field-row">
                <label class="field-label">常用镜像</label>
                <div class="preset-wrap">
                  <span v-for="m in mirrorPresets" :key="m" class="preset-btn" @click="form.githubMirror = m">{{ m }}</span>
                  <span class="preset-btn preset-clear" @click="form.githubMirror = ''" >清除</span>
                </div>
              </div>
            </div>
            <div class="content-title" style="margin-top:24px">内核管理</div>
            <div class="field-group">
              <div v-for="k in kernelList" :key="k.id" class="kernel-row">
                <div class="kernel-info">
                  <span class="kernel-name">{{ k.label }}</span>
                  <span :class="['kbadge', k.installed ? 'kbadge-ok' : 'kbadge-miss']">{{ kernelBadgeText(k) }}</span>
                  <span v-if="k.installed" class="kernel-version-text">
                    当前：{{ k.localVersion || '未知' }}<template v-if="k.latestVersion"> · 最新：{{ k.latestVersion }}</template>
                  </span>
                </div>
                <template v-if="kernelDownloading[k.id]">
                  <div class="dl-wrap"><div class="dl-bar"><div class="dl-fill" :style="{width: kernelDownloading[k.id].percent+'%'}"></div></div><span class="dl-msg">{{ kernelDownloading[k.id].message }}</span></div>
                </template>
                <button v-else :class="['btn','btn-sm', kernelActionClass(k)]" :disabled="k.installed && !k.hasUpdate" @click="downloadKernel(k.id)">{{ kernelActionText(k) }}</button>
              </div>
            </div>
          </div>
        </div>
      </div>
      <div v-if="msg" :class="['foot-msg', msg.error ? 'msg-err' : 'msg-ok']">{{ msg.text }}</div>
      <div class="modal-footer">
        <button class="btn btn-light" @click="$emit('close')">取消</button>
        <button class="btn btn-primary" :disabled="loading" @click="saveSetting">{{ loading ? '保存中...' : '保存' }}</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, reactive, onMounted } from 'vue'
import { api } from '../api'

const emit = defineEmits(['close', 'done'])
const loading = ref(false)
const msg = ref(null)
const activeTab = ref('general')

const tabs = [
  { id: 'general',  icon: '⚙️', label: '常规' },
  { id: 'download', icon: '📥', label: '下载' },
]

const form = reactive({
  logLevel: 'info',
  subscriptionAutoUpdateMode: 'off',
  subscriptionAutoUpdateIntervalHour: 12,
  transparentMode: 'close',
  maxLogLines: 500,
  maxLogFileSizeMB: 2,
  githubMirror: '',
})

const kernelList = ref([
  { id: 'singbox', label: 'sing-box', installed: false, localVersion: '', latestVersion: '', hasUpdate: false, checkError: '' },
  { id: 'xray',    label: 'Xray',     installed: false, localVersion: '', latestVersion: '', hasUpdate: false, checkError: '' },
  { id: 'v2ray',   label: 'V2Ray',    installed: false, localVersion: '', latestVersion: '', hasUpdate: false, checkError: '' },
])
const kernelDownloading = reactive({})
const mirrorPresets = ['https://ghfast.top', 'https://mirror.ghproxy.com', 'https://gh-proxy.com']
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
      maxLogLines: s.maxLogLines || 500,
      maxLogFileSizeMB: (s.maxLogFileSize || 2097152) / (1024 * 1024),
      githubMirror: s.githubMirror || '',
    })
  } catch (e) {
    msg.value = { error: true, text: '加载设置失败' }
  }
  await refreshKernelStatus()
})

async function downloadKernel(id) {
  const item = kernelList.value.find(k => k.id === id)
  if (item?.installed && !item.hasUpdate) return

  kernelDownloading[id] = { percent: 0, message: '准备中...' }
  try {
    await api.downloadKernel(id, (p) => { kernelDownloading[id] = { percent: p.percent, message: p.message } })
    await refreshKernelStatus()
  } catch (e) {
    msg.value = { error: true, text: id + ' 下载失败: ' + (e?.message || '未知错误') }
  } finally {
    delete kernelDownloading[id]
  }
}

async function refreshKernelStatus() {
  try {
    const { data } = await api.getKernelStatus()
    const kernels = data.kernels || {}
    const meta = data.kernelMeta || {}
    kernelList.value.forEach(k => {
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

function kernelActionText(k) {
  if (!k.installed) return '下载'
  if (k.hasUpdate) return '更新'
  return '已最新'
}

function kernelActionClass(k) {
  return k.installed ? 'btn-light' : 'btn-primary'
}

async function saveSetting() {
  loading.value = true
  msg.value = null
  try {
    await api.setSetting({
      ...fullSetting,
      logLevel: form.logLevel,
      subscriptionAutoUpdateMode: form.subscriptionAutoUpdateMode,
      subscriptionAutoUpdateIntervalHour: form.subscriptionAutoUpdateIntervalHour,
      transparentMode: form.transparentMode,
      maxLogLines: form.maxLogLines,
      maxLogFileSize: Math.round(form.maxLogFileSizeMB * 1024 * 1024),
      githubMirror: form.githubMirror,
    })
    msg.value = { error: false, text: '保存成功' }
    setTimeout(() => emit('done'), 800)
  } catch (e) {
    msg.value = { error: true, text: e?.response?.data?.error || '保存失败' }
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.setting-modal-box { width: 720px; max-height: 88vh; display: flex; flex-direction: column; }
.setting-layout { display: flex; flex: 1; overflow: hidden; }
.setting-nav {
  width: 140px; flex-shrink: 0;
  background: #f7f8fa; border-right: 1px solid #e8e8e8;
  padding: 12px 0;
}
.nav-item {
  display: flex; align-items: center; gap: 8px;
  padding: 10px 16px; font-size: 13px; cursor: pointer;
  color: #595959; border-left: 3px solid transparent;
  transition: all .15s;
}
.nav-item:hover { background: #efefef; color: #262626; }
.nav-active { background: #e6f0ff; color: #3273dc; border-left-color: #3273dc; font-weight: 600; }
.nav-icon { font-size: 15px; }
.setting-content { flex: 1; overflow-y: auto; padding: 24px; }
.content-title {
  font-size: 14px; font-weight: 700; color: #262626;
  margin-bottom: 16px; padding-bottom: 10px;
  border-bottom: 1px solid #e8e8e8;
}
.field-group {
  background: #fff; border: 1px solid #e8e8e8;
  border-radius: 8px; overflow: hidden;
}
.field-row {
  display: flex; align-items: center;
  padding: 12px 16px; border-bottom: 1px solid #f5f5f5;
  gap: 16px;
}
.field-row:last-child { border-bottom: none; }
.field-label { font-size: 13px; color: #595959; width: 160px; flex-shrink: 0; }
.field-input { flex: 1; max-width: 280px; }
.preset-wrap { display: flex; flex-wrap: wrap; gap: 6px; flex: 1; }
.preset-btn {
  font-size: 11px; padding: 3px 8px;
  background: #f0f0f0; border: 1px solid #d9d9d9;
  border-radius: 3px; cursor: pointer; transition: all .15s;
}
.preset-btn:hover { background: #e0e0e0; }
.preset-clear { color: #ff4d4f; }
.kernel-row {
  display: flex; align-items: center; justify-content: space-between;
  padding: 12px 16px; border-bottom: 1px solid #f5f5f5;
}
.kernel-row:last-child { border-bottom: none; }
.kernel-info { display: flex; align-items: center; gap: 8px; }
.kernel-name { font-size: 13px; font-weight: 500; }
.kernel-version-text { font-size: 12px; color: #8c8c8c; }
.kbadge { font-size: 11px; padding: 2px 7px; border-radius: 3px; }
.kbadge-ok { background: #f6ffed; color: #52c41a; border: 1px solid #b7eb8f; }
.kbadge-miss { background: #fff2f0; color: #ff4d4f; border: 1px solid #ffccc7; }
.dl-wrap { display: flex; flex-direction: column; gap: 3px; min-width: 180px; }
.dl-bar { height: 4px; background: #f0f0f0; border-radius: 2px; overflow: hidden; }
.dl-fill { height: 100%; background: #3273dc; transition: width .3s; }
.dl-msg { font-size: 11px; color: #8c8c8c; }
.input { display: block; width: 100%; padding: 6px 10px; border: 1px solid #dbdbdb; border-radius: 4px; font-size: 13px; color: #363636; background: #fff; outline: none; transition: border-color .15s; }
.input:focus { border-color: #3273dc; box-shadow: 0 0 0 2px rgba(50,115,220,.15); }
.btn { display: inline-flex; align-items: center; gap: 4px; padding: 6px 14px; border: 1px solid transparent; border-radius: 4px; font-size: 13px; cursor: pointer; transition: all .15s; white-space: nowrap; }
.btn-primary { background: #3273dc; color: #fff; border-color: #3273dc; }
.btn-primary:hover { background: #2366d1; }
.btn-light { background: #fff; color: #363636; border-color: #dbdbdb; }
.btn-light:hover { background: #f5f5f5; }
.btn-sm { padding: 3px 9px; font-size: 12px; }
.foot-msg { padding: 8px 20px; font-size: 13px; border-top: 1px solid #e8e8e8; }
.msg-ok { background: #f6ffed; color: #52c41a; }
.msg-err { background: #fff2f0; color: #ff4d4f; }
.modal-footer { display: flex; justify-content: flex-end; gap: 8px; padding: 12px 20px; border-top: 1px solid #dbdbdb; }
</style>
