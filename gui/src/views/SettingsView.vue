<template>
  <div class="settings-page">
    <div class="tab-bar-outer">
      <div class="tab-bar">
        <div class="tabs-wrap">
          <span :class="['tab', activeTab === 'kernel' ? 'tab-active' : '']" @click="activeTab = 'kernel'">内核和代理</span>
          <span :class="['tab', activeTab === 'inbounds' ? 'tab-active' : '']" @click="activeTab = 'inbounds'">出入站</span>
          <span :class="['tab', activeTab === 'rules' ? 'tab-active' : '']" @click="activeTab = 'rules'">分流规则</span>
          <span :class="['tab', activeTab === 'other' ? 'tab-active' : '']" @click="activeTab = 'other'">其他设置</span>
        </div>
      </div>
    </div>

    <div class="settings-content">
      <div v-if="activeTab === 'kernel'" class="data-files-bar">
        <span class="data-label">内核文件：</span>
        <span v-for="k in kernelFiles" :key="k.id" :class="['data-badge', k.installed ? 'data-ok' : 'data-missing']" :title="kernelBarTitle(k)">
          {{ k.label }} {{ kernelBarText(k) }}
        </span>
        <button class="action-btn" style="margin-left:auto" @click="showKernelModal = true">⬇ 下载/更新内核</button>
      </div>

      <div class="settings-block" v-if="activeTab === 'other'">
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

      <div class="settings-block" v-if="activeTab === 'kernel'">
        <div class="block-header">
          <span class="block-title">代理</span>
        </div>
        <div class="field-row">
          <label class="field-label">内核模式</label>
          <select class="input field-input" v-model="form.kernelMode">
            <option value="auto">自动选择</option>
            <option value="singbox">强制 sing-box</option>
            <option value="xray">强制 Xray</option>
            <option value="v2ray">强制 V2Ray</option>
          </select>
        </div>
        <div class="field-row field-row-tip">
          <label class="field-label"></label>
          <div class="field-tip">
            自动模式下，支持高级协议时优先使用 sing-box；若未安装 sing-box，则回退到 Xray。手动模式会严格使用你选择的内核。
          </div>
        </div>
        <div class="field-row">
          <label class="field-label">全局流量探测</label>
          <label class="toggle-wrap">
            <input type="checkbox" v-model="form.enableSniff" />
            <span>{{ form.enableSniff ? '已开启，按需识别 SNI/Host' : '已关闭，减少首包处理开销' }}</span>
          </label>
        </div>
        <div class="field-row">
          <label class="field-label">DNS 劫持</label>
          <label class="toggle-wrap" :class="{ 'toggle-wrap-disabled': hijackDNSUnsupported }">
            <input type="checkbox" v-model="form.enableHijackDNS" :disabled="hijackDNSUnsupported" />
            <span v-if="hijackDNSUnsupported">当前内核不支持，此项不生效</span>
            <span v-else>{{ form.enableHijackDNS ? '已开启，代理接管 DNS' : '已关闭，更多依赖系统/应用 DNS' }}</span>
          </label>
        </div>
        <div class="field-row field-row-tip">
          <label class="field-label"></label>
          <div class="field-tip">
            仅 sing-box 内核支持，Xray/V2Ray 下不生效。此项只拦截真正的 53 端口查询，用于透明代理或把系统 DNS 指向本机的场景；浏览器等应用经 HTTP/SOCKS 代理接入时自身不发 DNS 查询，不受此项影响。
          </div>
        </div>
        <div class="field-row">
          <label class="field-label">DNS 模式</label>
          <select class="input field-input" v-model="form.dnsMode">
            <option value="lightweight">轻量模式</option>
            <option value="compatible">兼容模式</option>
          </select>
        </div>
        <div class="field-row field-row-tip">
          <label class="field-label"></label>
          <div class="field-tip">
            轻量模式使用更直接的 UDP DNS，首包更快，适合浏览器代理等常见场景。兼容模式保留远端 DoH、bootstrap 和自动接口检测，DNS 接管更完整，但启动和访问首包会更重，更适合透明代理或复杂分流环境。
          </div>
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
          <label class="field-label">探测目标</label>
          <div style="flex:1; max-width:520px">
            <textarea class="input" v-model="form.probeTargets" rows="4" placeholder="每行一个域名，如&#10;www.gstatic.com&#10;www.google.com"></textarea>
            <div class="field-tip" style="margin-top:6px">严格探测模式下用于真实连接验证，支持逗号/空格/换行分隔。</div>
          </div>
        </div>
        <div class="field-row">
          <label class="field-label">分组测速间隔（秒）</label>
          <input type="number" class="input field-input" v-model.number="form.groupRealProbeIntervalSec" min="10" />
        </div>
        <div class="field-row">
          <label class="field-label">默认测速方式</label>
          <select class="input field-input" v-model="form.groupProbeMode">
            <option value="real">真连测试</option>
            <option value="fast">快速测试</option>
          </select>
        </div>
        <div class="field-row field-row-tip">
          <label class="field-label"></label>
          <div class="field-tip">用于分组周期测速和订阅更新后的自动测速；手动点击检测按钮不受影响。</div>
        </div>
        <div class="field-row">
          <label class="field-label">切换阈值（ms）</label>
          <input type="number" class="input field-input" v-model.number="form.groupSwitchThresholdMs" min="0" />
        </div>
        <div class="field-row">
          <label class="field-label">切换冷却时间（秒）</label>
          <input type="number" class="input field-input" v-model.number="form.groupSwitchCooldownSec" min="0" />
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

      <div class="settings-block" v-if="activeTab === 'other'">
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
        <div class="field-row field-row-tip">
          <label class="field-label">节点复制规则</label>
          <div class="copy-rules-wrap">
            <div class="field-tip">订阅分组自动更新并完成测速后，按规则向指定本地分组复制节点。目标分组已存在该节点时会跳过。</div>
            <div v-for="(rule, ri) in form.subscriptionBestNodeCopyRules" :key="ri" class="copy-rule-card">
              <div class="copy-rule-content">
                <div class="copy-rule-row">
                  <label class="copy-section-label">来源订阅分组</label>
                  <div class="copy-source-groups">
                    <label v-for="g in subscriptionGroups" :key="g.id" class="copy-source-item">
                      <input type="checkbox" :value="g.id" v-model="rule.sourceGroupIds" />
                      <span>{{ g.name }}</span>
                    </label>
                    <span v-if="subscriptionGroups.length === 0" class="empty-text">暂无订阅分组</span>
                  </div>
                </div>
                <div class="copy-rule-row">
                  <label class="copy-section-label">目标本地分组</label>
                  <div class="copy-target-groups">
                    <label v-for="g in localGroups" :key="g.id" class="copy-target-item">
                      <input type="checkbox" :value="g.id" v-model="rule.targetGroupIds" />
                      <span>{{ g.name }}</span>
                    </label>
                    <span v-if="localGroups.length === 0" class="empty-text">暂无本地分组</span>
                  </div>
                </div>
                <div class="copy-rule-row">
                  <label class="copy-section-label">复制数量</label>
                  <div class="copy-mode-wrap">
                    <label class="copy-mode-option">
                      <input type="radio" value="best" v-model="rule.mode" />
                      <span>仅延迟最低的 1 个</span>
                    </label>
                    <label class="copy-mode-option">
                      <input type="radio" value="topN" v-model="rule.mode" />
                      <span>延迟最低的前</span>
                      <input type="number" class="input copy-count-input" v-model.number="rule.count" min="1" :disabled="rule.mode !== 'topN'" />
                      <span>个</span>
                    </label>
                    <label class="copy-mode-option">
                      <input type="radio" value="all" v-model="rule.mode" />
                      <span>全部连通节点</span>
                    </label>
                  </div>
                </div>
              </div>
              <button class="action-btn action-btn-danger" @click="removeCopyRule(ri)">删除</button>
            </div>
            <button class="action-btn action-btn-add" @click="addCopyRule">+ 添加规则</button>
          </div>
        </div>
      </div>

      <div v-if="activeTab === 'inbounds'" class="settings-rules-host">
        <RulesView mode="inbounds" />
      </div>

      <div v-if="activeTab === 'rules'" class="settings-rules-host">
        <RulesView mode="rules" />
      </div>

      <!-- 保存按钮 -->
      <div class="save-bar" v-if="activeTab !== 'rules'">
        <button class="action-btn action-btn-primary" :disabled="saving" @click="saveSettings">
          {{ saving ? '保存中...' : '保存设置' }}
        </button>
        <span v-if="saveMsg" :class="['save-msg', saveMsg.error ? 'msg-error' : 'msg-success']">
          {{ saveMsg.text }}
        </span>
      </div>
    </div>
  </div>

  <!-- 内核下载弹窗 -->
  <div class="modal-overlay" v-if="showKernelModal" @mousedown.self="showKernelModal = false">
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
import { useProxyStore } from '../stores/proxy'
import RulesView from './RulesView.vue'

const saving = ref(false)
const saveMsg = ref(null)
const showKernelModal = ref(false)
const activeTab = ref('kernel')
const proxyStore = useProxyStore()

const form = reactive({
  logLevel: 'info',
  kernelMode: 'auto',
  enableSniff: false,
  enableHijackDNS: false,
  dnsMode: 'lightweight',
  subscriptionAutoUpdateMode: 'off',
  subscriptionAutoUpdateIntervalHour: 12,
  subscriptionBestNodeCopyRules: [],
  transparentMode: 'close',
  lanSharingEnabled: false,
  probeTargets: '',
  groupRealProbeIntervalSec: 300,
  groupProbeMode: 'real',
  groupSwitchThresholdMs: 100,
  groupSwitchCooldownSec: 600,
  socks5Port: 20260,
  httpPort: 20261,
  maxLogLines: 500,
  maxLogFileSizeMB: 2,
  githubMirror: '',
})

const kernelFiles = ref([
  { id: 'singbox', label: 'sing-box', desc: '主力内核，支持多协议', installed: false, localVersion: '', latestVersion: '', hasUpdate: false, checkError: '' },
  { id: 'xray',    label: 'Xray',     desc: 'XTLS/Xray-core',       installed: false, localVersion: '', latestVersion: '', hasUpdate: false, checkError: '' },
  { id: 'v2ray',   label: 'V2Ray',    desc: 'v2fly/v2ray-core',     installed: false, localVersion: '', latestVersion: '', hasUpdate: false, checkError: '' },
])
const kernelDownloading = reactive({})
const mirrorPresets = ['https://ghfast.top', 'https://mirror.ghproxy.com', 'https://gh-proxy.com']
const builtinListenHost = computed(() => form.lanSharingEnabled ? '0.0.0.0' : '127.0.0.1')
// DNS 劫持只在 sing-box 侧实现；auto 模式仍可能选中 sing-box，故不禁用
const hijackDNSUnsupported = computed(() => form.kernelMode === 'xray' || form.kernelMode === 'v2ray')
const groups = ref([])
const subscriptionGroups = computed(() => groups.value.filter(g => g.fromSub))
const localGroups = computed(() => groups.value.filter(g => !g.fromSub))
let fullSetting = {}

onMounted(async () => {
  try {
    const { data } = await api.getSetting()
    const s = data.setting
    fullSetting = { ...s }
    Object.assign(form, {
      logLevel: s.logLevel || 'info',
      kernelMode: s.kernelMode || 'auto',
      enableSniff: !!s.enableSniff,
      enableHijackDNS: !!s.enableHijackDNS,
      dnsMode: s.dnsMode || 'lightweight',
      subscriptionAutoUpdateMode: s.subscriptionAutoUpdateMode || 'off',
      subscriptionAutoUpdateIntervalHour: s.subscriptionAutoUpdateIntervalHour || 12,
      subscriptionBestNodeCopyRules: normalizeCopyRules(s.subscriptionBestNodeCopyRules),
      transparentMode: s.transparentMode || 'close',
      lanSharingEnabled: !!s.lanSharingEnabled,
      probeTargets: s.probeTargets || '',
      groupRealProbeIntervalSec: s.groupRealProbeIntervalSec || 300,
      groupProbeMode: s.groupProbeMode || 'real',
      groupSwitchThresholdMs: typeof s.groupSwitchThresholdMs === 'number' ? s.groupSwitchThresholdMs : 100,
      groupSwitchCooldownSec: typeof s.groupSwitchCooldownSec === 'number' ? s.groupSwitchCooldownSec : 600,
      socks5Port: s.socks5Port || 20260,
      httpPort: s.httpPort || 20261,
      maxLogLines: s.maxLogLines || 500,
      maxLogFileSizeMB: (s.maxLogFileSize || 2097152) / (1024 * 1024),
      githubMirror: s.githubMirror || '',
    })
  } catch {}
  await loadGroups()
  await refreshKernelStatus()
})

async function loadGroups() {
  try {
    const { data } = await api.getGroups()
    groups.value = data.groups || []
  } catch {
    groups.value = []
  }
}

function normalizeCopyRules(rules) {
  if (!Array.isArray(rules)) return []
  return rules.map(rule => {
    // 旧数据只有单个 sourceGroupId，折叠进 sourceGroupIds
    const sources = Array.isArray(rule?.sourceGroupIds) ? [...rule.sourceGroupIds] : []
    if (rule?.sourceGroupId && !sources.includes(rule.sourceGroupId)) {
      sources.push(rule.sourceGroupId)
    }
    const mode = ['best', 'topN', 'all'].includes(rule?.mode) ? rule.mode : 'best'
    return {
      sourceGroupIds: [...new Set(sources.filter(Boolean))],
      targetGroupIds: Array.isArray(rule?.targetGroupIds) ? [...new Set(rule.targetGroupIds.filter(Boolean))] : [],
      mode,
      count: mode === 'topN' ? (Number(rule?.count) > 0 ? Number(rule.count) : 3) : 0,
    }
  })
}

function sanitizeCopyRules(rules) {
  return normalizeCopyRules(rules)
    .map(rule => ({
      ...rule,
      // 复制到自身没有意义，且会让节点在同一分组里重复累积
      targetGroupIds: rule.targetGroupIds.filter(id => !rule.sourceGroupIds.includes(id)),
    }))
    .filter(rule => rule.sourceGroupIds.length > 0 && rule.targetGroupIds.length > 0)
}

function addCopyRule() {
  form.subscriptionBestNodeCopyRules.push({ sourceGroupIds: [], targetGroupIds: [], mode: 'best', count: 0 })
}

function removeCopyRule(index) {
  form.subscriptionBestNodeCopyRules.splice(index, 1)
}

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
  const copyRules = sanitizeCopyRules(form.subscriptionBestNodeCopyRules)
  try {
    const { data } = await api.setSetting({
      ...fullSetting,
      logLevel: form.logLevel,
      kernelMode: form.kernelMode,
      enableSniff: form.enableSniff,
      enableHijackDNS: form.enableHijackDNS,
      dnsMode: form.dnsMode,
      subscriptionAutoUpdateMode: form.subscriptionAutoUpdateMode,
      subscriptionAutoUpdateIntervalHour: form.subscriptionAutoUpdateIntervalHour,
      subscriptionBestNodeCopyRules: copyRules,
      transparentMode: form.transparentMode,
      lanSharingEnabled: form.lanSharingEnabled,
      probeTargets: form.probeTargets,
      groupRealProbeIntervalSec: form.groupRealProbeIntervalSec,
      groupProbeMode: form.groupProbeMode,
      groupSwitchThresholdMs: form.groupSwitchThresholdMs,
      groupSwitchCooldownSec: form.groupSwitchCooldownSec,
      maxLogLines: form.maxLogLines,
      maxLogFileSize: Math.round(form.maxLogFileSizeMB * 1024 * 1024),
      githubMirror: form.githubMirror,
    })
    // 设置接口在“保存成功但重启失败”时返回 200 + warning，需提示并刷新运行状态
    await proxyStore.fetchStatus()
    fullSetting = {
      ...fullSetting,
      logLevel: form.logLevel,
      kernelMode: form.kernelMode,
      enableSniff: form.enableSniff,
      enableHijackDNS: form.enableHijackDNS,
      dnsMode: form.dnsMode,
      subscriptionAutoUpdateMode: form.subscriptionAutoUpdateMode,
      subscriptionAutoUpdateIntervalHour: form.subscriptionAutoUpdateIntervalHour,
      subscriptionBestNodeCopyRules: copyRules,
      transparentMode: form.transparentMode,
      lanSharingEnabled: form.lanSharingEnabled,
      probeTargets: form.probeTargets,
      groupRealProbeIntervalSec: form.groupRealProbeIntervalSec,
      groupProbeMode: form.groupProbeMode,
      groupSwitchThresholdMs: form.groupSwitchThresholdMs,
      groupSwitchCooldownSec: form.groupSwitchCooldownSec,
      maxLogLines: form.maxLogLines,
      maxLogFileSize: Math.round(form.maxLogFileSizeMB * 1024 * 1024),
      githubMirror: form.githubMirror,
    }
    form.subscriptionBestNodeCopyRules = normalizeCopyRules(copyRules)
    if (data?.warning) {
      saveMsg.value = { error: true, text: data.warning }
    } else {
      saveMsg.value = { error: false, text: '保存成功' }
    }
    setTimeout(() => { saveMsg.value = null }, 3000)
  } catch (e) {
    saveMsg.value = { error: true, text: e?.response?.data?.error || '保存失败' }
  } finally {
    saving.value = false
  }
}
</script>

<style scoped>
.settings-page {
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.settings-content {
  max-width: 1200px;
  margin: 0 auto;
  padding: 0 24px;
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 12px;
}
.tab-bar-outer {
  background: #fff;
  border-bottom: 1px solid #e8e8e8;
  box-shadow: 0 1px 4px rgba(0,0,0,.04);
  flex-shrink: 0;
}
.tab-bar {
  display: flex;
  align-items: stretch;
  justify-content: space-between;
  max-width: 1200px;
  margin: 0 auto;
  padding: 0 24px;
  min-height: 46px;
}
.tabs-wrap {
  display: flex;
  align-items: stretch;
  overflow-x: auto;
  flex: 1;
  gap: 0;
}
.tabs-wrap::-webkit-scrollbar { height: 3px; }
.tabs-wrap::-webkit-scrollbar-thumb { background: #d9d9d9; border-radius: 2px; }
.tab {
  display: inline-flex;
  align-items: center;
  padding: 0 20px;
  height: 46px;
  cursor: pointer;
  font-size: 13px;
  color: #595959;
  border-bottom: 3px solid transparent;
  white-space: nowrap;
  transition: color .15s;
  flex-shrink: 0;
  letter-spacing: .01em;
}
.tab:hover { color: #1677ff; background: #f5f8ff; }
.tab-active {
  color: #1677ff;
  font-weight: 600;
  border-bottom-color: #1677ff;
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
.toggle-wrap-disabled { opacity: .6; cursor: not-allowed; }
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
.copy-rules-wrap {
  display: flex;
  flex-direction: column;
  /* 不设 align-items 时默认 stretch，会把按钮拉满整行宽度 */
  align-items: flex-start;
  gap: 10px;
  flex: 1;
  max-width: 760px;
}
.copy-rule-card {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  padding: 10px;
  border: 1px solid #f0f0f0;
  border-radius: 8px;
  background: #fafafa;
  width: 100%;
  box-sizing: border-box;
}
.copy-rule-content {
  display: flex;
  flex-direction: column;
  gap: 10px;
  flex: 1;
  min-width: 0;
}
.copy-rule-row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
}
.copy-section-label {
  font-size: 12px;
  color: #8c8c8c;
  width: 96px;
  flex-shrink: 0;
  padding-top: 5px;
}
.copy-source-groups,
.copy-target-groups {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  flex: 1;
  min-width: 0;
}
.copy-source-item,
.copy-target-item {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 8px;
  border: 1px solid #e8e8e8;
  border-radius: 999px;
  background: #fff;
  font-size: 12px;
  color: #595959;
  cursor: pointer;
}
.copy-mode-wrap {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 14px;
  flex: 1;
  min-width: 0;
}
.copy-mode-option {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  font-size: 12px;
  color: #595959;
  cursor: pointer;
}
.copy-count-input {
  width: 64px;
  display: inline-block;
  padding: 3px 6px;
}
.action-btn-add { flex-shrink: 0; }
.action-btn-danger {
  flex-shrink: 0;
  border-color: #ffccc7;
  color: #ff4d4f;
}
.action-btn-danger:hover {
  border-color: #ff4d4f;
  color: #ff4d4f;
  background: #fff2f0;
}
.empty-text {
  font-size: 12px;
  color: #bfbfbf;
  line-height: 30px;
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

@media (max-width: 900px) {
  .settings-page {
    gap: 10px;
  }
  .settings-content {
    padding: 0 10px;
    gap: 10px;
  }
  .tab-bar {
    padding: 0 10px;
  }
  .tab {
    padding: 0 14px;
  }
  .data-files-bar {
    flex-wrap: wrap;
  }
  .field-row {
    align-items: flex-start;
    flex-direction: column;
    gap: 8px;
  }
  .field-label {
    width: 100%;
  }
  .field-input {
    max-width: 100%;
    width: 100%;
  }
  .data-file-row {
    flex-direction: column;
    align-items: flex-start;
    gap: 8px;
  }
}

.settings-rules-host :deep(.rules-page) {
  padding: 0;
  max-width: 100%;
  margin: 0;
}
</style>

