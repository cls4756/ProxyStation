<template>
  <div class="home">
    <!-- Tab 栏 -->
    <div class="tab-bar-outer">
      <div class="tab-bar">
        <div class="tabs-wrap">
          <span
            v-for="(group, gi) in manualGroups" :key="'group-' + group.id"
            :class="['tab', activeTab === 'group-' + gi ? 'tab-active' : '']"
            @click="activeTab = 'group-' + gi"
          >
            {{ group.name }}
            <span class="tab-badge">{{ group.servers?.length || 0 }}</span>
          </span>
          <span
            v-for="(sub, si) in store.subscriptions" :key="'sub-' + sub.id"
            :class="['tab', activeTab === 'sub-' + si ? 'tab-active' : '']"
            @click="activeTab = 'sub-' + si"
          >
            {{ sub.name || sub.host }}
            <span class="tab-badge">{{ sub.servers?.length || 0 }}</span>
          </span>
        </div>
        <div class="tab-actions">
          <input
            class="search-input"
            v-model="searchQuery"
            placeholder="搜索节点…"
            @input="onSearch"
          />
          <button class="action-btn" @click="pingCurrentTab" :disabled="pinging">
            ⚡ {{ pinging ? '测速中…' : '测速' }}
          </button>
          <button class="action-btn action-btn-primary" @click="openImport">＋ 导入</button>
          <button class="action-btn" @click="showNewGroup = true">📁 新建分组</button>
        </div>
      </div>
    </div>

    <!-- 当前出站状态条已移至 App.vue 全局显示 -->
    <div class="content-area">

      <!-- 多选工具栏（有选中时显示） -->
      <div class="bulk-bar" v-if="selectedRefs.length || showSelectAll">
        <span class="bulk-count">已选 {{ selectedRefs.length }} 个节点</span>
        <button class="action-btn" @click="selectAll" v-if="!allSelected">☑ 全选</button>
        <button class="action-btn" @click="selectedRefs = []" v-if="allSelected">☐ 取消全选</button>
        <button class="action-btn" @click="pingSelected" :disabled="pinging">⚡ 批量测速</button>
        <button class="action-btn" @click="shareSelected">🔗 批量分享</button>
        <button class="action-btn" @click="showBulkCopyModal = true">📋 复制到分组</button>
        <button v-if="canBulkRemove" class="action-btn" @click="showBulkMoveModal = true">✂ 移动到分组</button>
        <button v-if="canBulkRemove" class="action-btn action-btn-danger" @click="removeSelected">✕ 批量删除</button>
        <button class="action-btn" @click="selectedRefs = []">取消选择</button>
      </div>

      <!-- 搜索结果面板 -->
      <div v-if="isSearching" class="panel" style="margin-bottom:0">
        <div class="info-bar">
          <span style="font-weight:600">搜索结果</span>
          <span class="info-count">{{ searchResults.length }} 个节点</span>
          <button class="action-btn" style="margin-left:auto" @click="searchQuery=''; isSearching=false">✕ 关闭</button>
        </div>
        <div v-if="!searchResults.length" class="empty-hint" style="padding:20px 0">
          <p class="empty-sub">无匹配节点</p>
        </div>
        <ServerRow
          v-for="(r, i) in searchResults" :key="i"
          :server="r.server" :ref-obj="r.ref" :outbounds="store.outbounds" :removable="false"
          :current-outbound="currentOutbound"
          :selected="isSelected(r.ref)"
          @connect="connectNode" 
          @select="toggleSelect"
          @share="(ref, sv) => openNodeMenu('share', ref, sv)"
        />
      </div>

      <!-- 订阅 tab -->
      <template v-for="(sub, si) in store.subscriptions" :key="'sub-' + sub.id">
        <div v-if="activeTab === 'sub-' + si" class="panel">
          <div class="info-bar">
            <div class="info-bar-left">
              <span class="info-url" :title="sub.url">{{ sub.url }}</span>
              <span v-if="sub.updatedAt" class="info-time">{{ formatTime(sub.updatedAt) }}</span>
            </div>
            <div class="info-bar-right">
              <button class="action-btn" @click="refreshSub(si)" title="重新拉取订阅节点">↻ 更新节点</button>
              <button class="action-btn" @click="openEditSub(si)" title="编辑订阅名称和链接">✏ 编辑</button>
              <button class="action-btn action-btn-danger" @click="deleteSub(si)">删除</button>
            </div>
          </div>
          <div v-if="!sub.servers?.length" class="empty-hint">
            <p class="empty-sub">订阅为空，点击「更新节点」拉取</p>
          </div>
          <ServerRow
            v-for="(s, i) in sub.servers" :key="i"
            :server="s" :ref-obj="{ type: 'sub_server', index: i, sub: si }"
            :outbounds="store.outbounds" :removable="false"
            :current-outbound="currentOutbound"
            :selected="isSelected({ type: 'sub_server', index: i, sub: si })"
            @connect="connectNode"
            @select="toggleSelect"
            @share="(r, sv) => openNodeMenu('share', r, sv)"
            @copy-to-group="ref => openSingleCopy(ref)"
          />
        </div>
      </template>

      <!-- 手动分组 tab -->
      <template v-for="(group, gi) in manualGroups" :key="'group-' + group.id">
        <div v-if="activeTab === 'group-' + gi" class="panel">
          <div class="info-bar">
            <div class="info-bar-left">
              <span class="group-name-display">{{ group.name }}</span>
              <span class="info-count">{{ getGroupServers(group).length }} 个节点</span>
            </div>
            <div class="info-bar-right">
              <button v-if="group.name !== 'SERVER'" class="action-btn" @click="openEditGroup(gi)">✏ 编辑</button>
              <button class="action-btn" @click="openAddToGroup(gi)">＋ 添加节点</button>
              <button v-if="group.name !== 'SERVER'" class="action-btn action-btn-danger" @click="deleteGroup(gi)">删除分组</button>
            </div>
          </div>
          <div v-if="!getGroupServers(group).length" class="empty-hint">
            <p class="empty-sub">分组为空，点击「添加节点」</p>
          </div>
          <ServerRow
            v-for="(s, i) in getGroupServers(group)" :key="i"
            :server="s" :ref-obj="group.servers[i]" :outbounds="store.outbounds" :removable="true"
            :current-outbound="currentOutbound"
            :selected="isSelected(group.servers[i])"
            @connect="connectNode"
            @select="toggleSelect"
            @remove="ref => removeFromGroup(realGroupIndex(group), ref)"
            @edit="(r, sv) => openNodeMenu('edit', r, sv)"
            @share="(r, sv) => openNodeMenu('share', r, sv)"
            @copy-to-group="ref => openSingleCopy(ref)"
            @move-to-group="ref => openSingleMove(ref)"
          />
        </div>
      </template>

    </div>

    <!-- NodeMenu 弹窗 -->
    <NodeMenu
      v-if="nodeMenuMode"
      :mode="nodeMenuMode"
      :ref-obj="nodeMenuRef"
      :server="nodeMenuServer"
      @close="nodeMenuMode = ''"
      @updated="store.fetchAll()"
    />

    <!-- 新建分组弹窗 -->
    <div class="modal-overlay" v-if="showNewGroup" @click.self="showNewGroup = false">
      <div class="modal-box" style="width:380px">
        <div class="modal-header">
          <span class="modal-title">新建分组</span>
          <span class="modal-close" @click="showNewGroup = false">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">分组名称</label>
            <input class="input" v-model="newGroupName" placeholder="我的分组" @keyup.enter="createGroup" autofocus />
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="showNewGroup = false">取消</button>
          <button class="btn btn-primary" @click="createGroup">创建</button>
        </div>
      </div>
    </div>

    <!-- 编辑分组弹窗 -->
    <div class="modal-overlay" v-if="editGroupIdx >= 0" @click.self="editGroupIdx = -1">
      <div class="modal-box" style="width:380px">
        <div class="modal-header">
          <span class="modal-title">编辑分组</span>
          <span class="modal-close" @click="editGroupIdx = -1">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">分组名称</label>
            <input class="input" v-model="editGroupName" @keyup.enter="saveEditGroup" autofocus />
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="editGroupIdx = -1">取消</button>
          <button class="btn btn-primary" @click="saveEditGroup">保存</button>
        </div>
      </div>
    </div>

    <!-- 添加节点到分组弹窗 -->
    <div class="modal-overlay" v-if="showAddToGroup >= 0" @click.self="showAddToGroup = -1">
      <div class="modal-box">
        <div class="modal-header">
          <span class="modal-title">添加节点到「{{ manualGroups[showAddToGroup]?.name }}」</span>
          <span class="modal-close" @click="showAddToGroup = -1">✕</span>
        </div>
        <div style="padding:8px 16px; border-bottom:1px solid #f0f0f0">
          <input class="input" v-model="addSearch" placeholder="搜索节点名称…" style="font-size:12px" />
        </div>
        <div class="modal-body" style="padding:0; max-height:400px; overflow-y:auto">
          <div
            v-for="(s, i) in filteredSelectableServers" :key="i"
            :class="['sel-row', selectedAddIdx === i ? 'sel-row-active' : '']"
            @click="selectedAddIdx = i"
          >
            <span :class="['tag', `tag-${s.type || 'vmess'}`]">{{ s.type || '?' }}</span>
            <span class="sel-name">{{ s.name || '未命名' }}</span>
            <span class="sel-addr">{{ s.host }}:{{ s.port }}</span>
          </div>
          <div v-if="!filteredSelectableServers.length" style="padding:20px; text-align:center; color:#b5b5b5; font-size:13px">
            无匹配节点
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="showAddToGroup = -1">取消</button>
          <button class="btn btn-primary" :disabled="selectedAddIdx < 0" @click="confirmAddToGroup">添加</button>
        </div>
      </div>
    </div>

    <!-- 编辑订阅弹窗 -->
    <div class="modal-overlay" v-if="editSubIdx >= 0" @click.self="editSubIdx = -1">
      <div class="modal-box" style="width:480px">
        <div class="modal-header">
          <span class="modal-title">编辑订阅</span>
          <span class="modal-close" @click="editSubIdx = -1">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">订阅名称</label>
            <input class="input" v-model="editSubForm.name" placeholder="订阅名称" />
          </div>
          <div class="form-group">
            <label class="form-label">订阅链接</label>
            <input class="input" v-model="editSubForm.url" placeholder="https://..." />
          </div>
          <div class="form-group">
            <label class="form-label">格式</label>
            <select class="input" v-model="editSubForm.format">
              <option value="auto">自动检测</option>
              <option value="v2ray">V2Ray（base64）</option>
              <option value="clash">Clash YAML</option>
              <option value="singbox">sing-box JSON</option>
            </select>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="editSubIdx = -1">取消</button>
          <button class="btn btn-primary" @click="saveEditSub">保存</button>
        </div>
      </div>
    </div>

    <!-- 批量复制到分组弹窗 -->
    <div class="modal-overlay" v-if="showBulkCopyModal" @click.self="showBulkCopyModal = false">
      <div class="modal-box" style="width:380px">
        <div class="modal-header">
          <span class="modal-title">复制到分组</span>
          <span class="modal-close" @click="showBulkCopyModal = false">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">选择分组</label>
            <select class="input" v-model="bulkCopyGroupId">
              <option value="">-- 请选择 --</option>
              <option v-for="g in manualGroups" :key="g.id" :value="g.id">
                {{ g.name }}（{{ g.servers?.length || 0 }} 个节点）
              </option>
            </select>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="showBulkCopyModal = false">取消</button>
          <button class="btn btn-primary" :disabled="!bulkCopyGroupId" @click="confirmBulkCopy">复制</button>
        </div>
      </div>
    </div>

    <!-- 批量移动到分组弹窗 -->
    <div class="modal-overlay" v-if="showBulkMoveModal" @click.self="showBulkMoveModal = false">
      <div class="modal-box" style="width:380px">
        <div class="modal-header">
          <span class="modal-title">移动到分组</span>
          <span class="modal-close" @click="showBulkMoveModal = false">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">选择目标分组</label>
            <select class="input" v-model="bulkMoveGroupId">
              <option value="">-- 请选择 --</option>
              <option v-for="g in manualGroups" :key="g.id" :value="g.id">
                {{ g.name }}（{{ g.servers?.length || 0 }} 个节点）
              </option>
            </select>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="showBulkMoveModal = false">取消</button>
          <button class="btn btn-primary" :disabled="!bulkMoveGroupId" @click="confirmBulkMove">移动</button>
        </div>
      </div>
    </div>

    <!-- 单节点复制到分组弹窗 -->
    <div class="modal-overlay" v-if="showSingleCopyModal" @click.self="showSingleCopyModal = false">
      <div class="modal-box" style="width:360px">
        <div class="modal-header">
          <span class="modal-title">复制节点到分组</span>
          <span class="modal-close" @click="showSingleCopyModal = false">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">选择目标分组</label>
            <select class="input" v-model="singleTargetGroupId">
              <option value="">-- 请选择 --</option>
              <option v-for="g in manualGroups" :key="g.id" :value="g.id">{{ g.name }}</option>
            </select>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="showSingleCopyModal = false">取消</button>
          <button class="btn btn-primary" :disabled="!singleTargetGroupId" @click="confirmSingleCopy">复制</button>
        </div>
      </div>
    </div>

    <!-- 单节点移动到分组弹窗 -->
    <div class="modal-overlay" v-if="showSingleMoveModal" @click.self="showSingleMoveModal = false">
      <div class="modal-box" style="width:360px">
        <div class="modal-header">
          <span class="modal-title">移动节点到分组</span>
          <span class="modal-close" @click="showSingleMoveModal = false">✕</span>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">选择目标分组</label>
            <select class="input" v-model="singleTargetGroupId">
              <option value="">-- 请选择 --</option>
              <option v-for="g in manualGroups" :key="g.id" :value="g.id">{{ g.name }}</option>
            </select>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="showSingleMoveModal = false">取消</button>
          <button class="btn btn-primary" :disabled="!singleTargetGroupId" @click="confirmSingleMove">移动</button>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, inject, watch } from 'vue'
import { useProxyStore } from '../stores/proxy'
import { api } from '../api'
import ServerRow from '../components/ServerRow.vue'
import NodeMenu from '../components/NodeMenu.vue'

const store = useProxyStore()
const openImport = inject('openImport')
const currentOutbound = inject('currentOutbound')

const activeTab = ref('group-0')
const pinging = ref(false)
const showNewGroup = ref(false)
const newGroupName = ref('')
const editGroupIdx = ref(-1)
const editGroupName = ref('')
const showAddToGroup = ref(-1)
const selectedAddIdx = ref(-1)
const addSearch = ref('')
const searchQuery = ref('')
const searchResults = ref([])
const isSearching = ref(false)

// 订阅编辑状态
const editSubIdx = ref(-1)
const editSubForm = ref({ name: '', url: '', format: 'auto' })

// 批量复制到分组
const showBulkCopyModal = ref(false)
const bulkCopyGroupId = ref('')

// NodeMenu 状态
const nodeMenuMode = ref('')
const nodeMenuRef = ref(null)
const nodeMenuServer = ref(null)

// 多选状态
const selectedRefs = ref([])

function refKey(ref) {
  return `${ref.type}:${ref.index}:${ref.sub || 0}`
}
function isSelected(ref) {
  return selectedRefs.value.some(r => refKey(r) === refKey(ref))
}
function toggleSelect(ref, checked) {
  if (checked) {
    if (!isSelected(ref)) selectedRefs.value.push(ref)
  } else {
    selectedRefs.value = selectedRefs.value.filter(r => refKey(r) !== refKey(ref))
  }
}

// 全选功能
const showSelectAll = ref(false)
const allSelected = computed(() => {
  if (!selectedRefs.value.length) return false
  const currentTabServers = getCurrentTabServers()
  return selectedRefs.value.length === currentTabServers.length
})

function getCurrentTabServers() {
  if (activeTab.value.startsWith('sub-')) {
    const si = parseInt(activeTab.value.replace('sub-', ''))
    const sub = store.subscriptions[si]
    return (sub?.servers || []).map((_, i) => ({ type: 'sub_server', index: i, sub: si }))
  }
  if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    return (group?.servers || []).map(ref => ref)
  }
  return []
}

function selectAll() {
  const currentTabServers = getCurrentTabServers()
  selectedRefs.value = [...currentTabServers]
  showSelectAll.value = true
}

// 切换 tab 时清空选择
watch(activeTab, () => { selectedRefs.value = []; showSelectAll.value = false })

// 当前 tab 是否可批量删除（只有手动分组可删）
const canBulkRemove = computed(() => {
  return activeTab.value.startsWith('group-')
})

async function pingSelected() {
  if (!selectedRefs.value.length) return
  pinging.value = true
  try {
    await api.pingNodes(selectedRefs.value.map(r => ({ type: r.type, index: r.index, sub: r.sub || 0 })))
    await store.fetchAll()
  } finally {
    pinging.value = false
  }
}

async function shareSelected() {
  // 批量分享：逐个获取链接，拼成多行文本复制到剪贴板
  const links = []
  for (const ref of selectedRefs.value) {
    try {
      const { data } = await api.getServerLink(ref.type, ref.index, ref.sub || 0)
      if (data.link) links.push(data.link)
    } catch {}
  }
  if (links.length) {
    await navigator.clipboard.writeText(links.join('\n'))
    alert(`已复制 ${links.length} 个节点链接到剪贴板`)
  }
}

async function removeSelected() {
  if (!selectedRefs.value.length) return
  if (!confirm(`确认删除选中的 ${selectedRefs.value.length} 个节点？`)) return
  if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    for (const ref of selectedRefs.value) {
      await api.removeServerFromGroup(realGroupIndex(group), ref)
    }
  }
  selectedRefs.value = []
  await store.fetchAll()
}

function openNodeMenu(mode, refObj, server) {
  nodeMenuRef.value = refObj
  nodeMenuServer.value = server
  nodeMenuMode.value = mode
}

let searchTimer = null
async function onSearch() {
  if (!searchQuery.value.trim()) {
    isSearching.value = false
    searchResults.value = []
    return
  }
  clearTimeout(searchTimer)
  searchTimer = setTimeout(async () => {
    isSearching.value = true
    try {
      const { data } = await api.searchServers(searchQuery.value.trim())
      searchResults.value = data.results || []
    } catch {
      searchResults.value = []
    }
  }, 300)
}

const manualServers = computed(() =>
  store.servers.filter(s => !s.source || s.source === 'manual')
)
const manualRefs = computed(() =>
  manualServers.value.map((_, i) => ({
    type: 'server',
    index: store.servers.indexOf(manualServers.value[i]),
    sub: 0
  }))
)
const manualGroups = computed(() => {
  const groups = store.groups.filter(g => !g.fromSub)
  // 分离 SERVER 分组和其他分组
  const serverGroup = groups.find(g => g.name === 'SERVER')
  const otherGroups = groups.filter(g => g.name !== 'SERVER')
  // 其他分组按创建时间排序
  otherGroups.sort((a, b) => {
    const timeA = new Date(a.createdAt || 0).getTime()
    const timeB = new Date(b.createdAt || 0).getTime()
    return timeA - timeB
  })
  // SERVER 分组始终排在最前
  return serverGroup ? [serverGroup, ...otherGroups] : otherGroups
})

const proxyOutbound = computed(() => store.outbounds.find(o => o.name === 'proxy'))

// 当前 tab 对应的分组（如果是分组 tab）
const currentTabGroup = computed(() => {
  if (!activeTab.value.startsWith('group-')) return null
  const gi = parseInt(activeTab.value.replace('group-', ''))
  return manualGroups.value[gi] || null
})

function getGroupName(id) {
  return store.groups.find(g => g.id === id)?.name || id
}

function getNodeNameByRef(ref) {
  if (!ref) return '未知'
  if (ref.type === 'server') return store.servers[ref.index]?.name || '未知'
  return store.subscriptions[ref.sub]?.servers?.[ref.index]?.name || '未知'
}

// 把当前 tab 的分组设为 proxy 出站的分组（自动选优模式）
async function switchToGroupMode() {
  const group = currentTabGroup.value
  if (!group) return
  await api.connectOutbound('proxy', {
    targetType: 'group',
    groupId: group.id,
    mode: 'leastping',
  })
  await store.fetchAll()
  // 刷新代理状态，因为 connectOutbound 可能会触发 Restart
  await store.fetchStatus()
}

function realGroupIndex(group) {
  return store.groups.findIndex(g => g.id === group.id)
}

function getGroupServers(group) {
  return (group.servers || []).map(ref => {
    if (ref.type === 'server') return store.servers[ref.index]
    const sub = store.subscriptions[ref.sub]
    return sub?.servers?.[ref.index]
  }).filter(Boolean)
}

const allSelectableServers = computed(() => {
  const result = []
  store.servers.forEach((s, i) => result.push({ ...s, _ref: { type: 'server', index: i, sub: 0 } }))
  store.subscriptions.forEach((sub, si) => {
    sub.servers?.forEach((s, i) => result.push({ ...s, _ref: { type: 'sub_server', index: i, sub: si } }))
  })
  return result
})

const filteredSelectableServers = computed(() => {
  if (!addSearch.value.trim()) return allSelectableServers.value
  const q = addSearch.value.toLowerCase()
  return allSelectableServers.value.filter(s =>
    (s.name || '').toLowerCase().includes(q) ||
    (s.host || '').toLowerCase().includes(q)
  )
})

function formatTime(t) {
  if (!t) return ''
  return new Date(t).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

async function connectNode(ref) {
  const outbound = store.outbounds.find(o => o.name === currentOutbound.value)
  const t = outbound?.target

  // 当前是分组模式，点击节点会切换为手动模式，给出提示
  if (t?.targetType === 'group') {
    const groupName = store.groups.find(g => g.id === t.groupId)?.name || t.groupId
    if (!confirm(`当前出站已绑定分组「${groupName}」（自动选优）。\n点击确认将切换为手动指定此节点。`)) {
      return
    }
  }

  const active = t?.nodeRef
  if (active?.type === ref.type && active?.index === ref.index && active?.sub === (ref.sub || 0)) {
    await api.disconnectOutbound(currentOutbound.value)
  } else {
    await api.connectOutbound(currentOutbound.value, { targetType: 'node', nodeRef: { type: ref.type, index: ref.index, sub: ref.sub || 0 } })
  }
  await store.fetchAll()
  // 刷新代理状态，因为 connectOutbound 可能会触发 Restart
  await store.fetchStatus()
}

async function pingSingleNode(ref) {
  await api.pingNodes([{ type: ref.type, index: ref.index, sub: ref.sub || 0 }])
  await store.fetchAll()
}

async function pingCurrentTab() {
  pinging.value = true
  try {
    if (activeTab.value.startsWith('sub-')) {
      const si = parseInt(activeTab.value.replace('sub-', ''))
      const sub = store.subscriptions[si]
      if (sub?.servers?.length) {
        const refs = sub.servers.map((_, i) => ({ type: 'sub_server', index: i, sub: si }))
        await api.pingNodes(refs)
      }
    } else if (activeTab.value.startsWith('group-')) {
      const gi = parseInt(activeTab.value.replace('group-', ''))
      const group = manualGroups.value[gi]
      if (group) await api.pingGroup(realGroupIndex(group))
    }
    await store.fetchAll()
  } finally {
    pinging.value = false
  }
}

async function removeManualServer(ref) {
  await api.deleteServers([ref.index])
  await store.fetchAll()
}

async function removeFromGroup(groupIndex, ref) {
  await api.removeServerFromGroup(groupIndex, ref)
  await store.fetchAll()
}

async function refreshSub(si) {
  await api.refreshSubscription(si)
  await store.fetchAll()
}

function openEditSub(si) {
  const sub = store.subscriptions[si]
  if (!sub) return
  editSubIdx.value = si
  editSubForm.value = { name: sub.name || '', url: sub.url || '', format: sub.format || 'auto' }
}

async function saveEditSub() {
  if (editSubIdx.value < 0) return
  await api.updateSubscription(editSubIdx.value, editSubForm.value)
  editSubIdx.value = -1
  await store.fetchAll()
}

async function deleteSub(si) {
  await api.deleteSubscription(si)
  activeTab.value = 'group-0'
  await store.fetchAll()
}

async function deleteGroup(gi) {
  const group = manualGroups.value[gi]
  await api.deleteGroup(realGroupIndex(group))
  activeTab.value = 'group-0'
  await store.fetchAll()
}

async function createGroup() {
  if (!newGroupName.value.trim()) return
  await api.createGroup(newGroupName.value.trim())
  showNewGroup.value = false
  newGroupName.value = ''
  await store.fetchAll()
  activeTab.value = 'group-' + (manualGroups.value.length - 1)
}

function openEditGroup(gi) {
  editGroupIdx.value = gi
  editGroupName.value = manualGroups.value[gi]?.name || ''
}

async function saveEditGroup() {
  if (!editGroupName.value.trim() || editGroupIdx.value < 0) return
  const group = manualGroups.value[editGroupIdx.value]
  await api.updateGroup(realGroupIndex(group), editGroupName.value.trim())
  editGroupIdx.value = -1
  await store.fetchAll()
}

function openAddToGroup(gi) {
  showAddToGroup.value = gi
  selectedAddIdx.value = -1
  addSearch.value = ''
}

async function confirmAddToGroup() {
  if (selectedAddIdx.value < 0) return
  const s = filteredSelectableServers.value[selectedAddIdx.value]
  const group = manualGroups.value[showAddToGroup.value]
  await api.addServerToGroup(realGroupIndex(group), s._ref)
  showAddToGroup.value = -1
  selectedAddIdx.value = -1
  await store.fetchAll()
}

async function confirmBulkCopy() {
  if (!bulkCopyGroupId.value || !selectedRefs.value.length) return
  for (const ref of selectedRefs.value) {
    await api.copyServerToGroup(ref, bulkCopyGroupId.value)
  }
  showBulkCopyModal.value = false
  bulkCopyGroupId.value = ''
  selectedRefs.value = []
  await store.fetchAll()
}

// 移动到分组（从当前分组移除，加入目标分组）
const showBulkMoveModal = ref(false)
const bulkMoveGroupId = ref('')
const singleMoveRef = ref(null)
const singleCopyRef = ref(null)
const showSingleCopyModal = ref(false)
const showSingleMoveModal = ref(false)
const singleTargetGroupId = ref('')

async function confirmBulkMove() {
  if (!bulkMoveGroupId.value || !selectedRefs.value.length) return
  // 先复制到目标分组
  for (const ref of selectedRefs.value) {
    await api.copyServerToGroup(ref, bulkMoveGroupId.value)
  }
  // 再从当前分组移除
  if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    for (const ref of selectedRefs.value) {
      await api.removeServerFromGroup(realGroupIndex(group), ref)
    }
  }
  showBulkMoveModal.value = false
  bulkMoveGroupId.value = ''
  selectedRefs.value = []
  await store.fetchAll()
}

function openSingleCopy(ref) {
  singleCopyRef.value = ref
  singleTargetGroupId.value = ''
  showSingleCopyModal.value = true
}

function openSingleMove(ref) {
  singleMoveRef.value = ref
  singleTargetGroupId.value = ''
  showSingleMoveModal.value = true
}

async function confirmSingleCopy() {
  if (!singleTargetGroupId.value || !singleCopyRef.value) return
  await api.copyServerToGroup(singleCopyRef.value, singleTargetGroupId.value)
  showSingleCopyModal.value = false
  await store.fetchAll()
}

async function confirmSingleMove() {
  if (!singleTargetGroupId.value || !singleMoveRef.value) return
  await api.copyServerToGroup(singleMoveRef.value, singleTargetGroupId.value)
  // 从当前分组移除
  if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    await api.removeServerFromGroup(realGroupIndex(group), singleMoveRef.value)
  }
  showSingleMoveModal.value = false
  await store.fetchAll()
}</script>

<style scoped>
.home { display: flex; flex-direction: column; height: 100%; background: #f0f2f5; }

/* ===== Tab 栏外层：全宽白底 ===== */
.tab-bar-outer {
  background: #fff;
  border-bottom: 1px solid #e8e8e8;
  box-shadow: 0 1px 4px rgba(0,0,0,.04);
  flex-shrink: 0;
}

/* ===== Tab 栏内层：居中，两边留白 ===== */
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
  gap: 6px;
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
.tab-active { color: #1677ff; border-bottom-color: #1677ff; font-weight: 600; }
.tab-badge {
  background: #f0f0f0;
  color: #8c8c8c;
  border-radius: 10px;
  font-size: 10px;
  font-weight: 600;
  padding: 1px 6px;
  min-width: 18px;
  text-align: center;
  line-height: 16px;
}
.tab-active .tab-badge { background: #e6f0ff; color: #1677ff; }

.tab-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  padding-left: 16px;
  flex-shrink: 0;
}

.search-input {
  height: 28px;
  padding: 0 10px;
  border: 1px solid #d9d9d9;
  border-radius: 14px;
  font-size: 12px;
  outline: none;
  width: 140px;
  transition: all .2s;
  background: #fafafa;
  color: #262626;
}
.search-input:focus {
  border-color: #1677ff;
  background: #fff;
  width: 200px;
  box-shadow: 0 0 0 2px rgba(22,119,255,.1);
}

/* 操作按钮（比 .btn 更轻量） */
.action-btn {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 5px 12px;
  border: 1px solid #d9d9d9;
  border-radius: 6px;
  background: #fff;
  color: #595959;
  font-size: 12px;
  cursor: pointer;
  transition: all .15s;
  white-space: nowrap;
}
.action-btn:hover { border-color: #1677ff; color: #1677ff; background: #f5f8ff; }
.action-btn:disabled { opacity: .5; cursor: not-allowed; }
.action-btn-primary { background: #1677ff; color: #fff; border-color: #1677ff; }
.action-btn-primary:hover { background: #0958d9; border-color: #0958d9; color: #fff; }
.action-btn-danger { color: #ff4d4f; border-color: #ff4d4f; }
.action-btn-danger:hover { background: #fff1f0; }

/* ===== 多选工具栏 ===== */
.bulk-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 24px;
  background: #e6f4ff;
  border-bottom: 1px solid #bae0ff;
  flex-shrink: 0;
  margin-bottom: 0;
}
.bulk-count {
  font-size: 13px;
  font-weight: 600;
  color: #1677ff;
  margin-right: 4px;
}
.content-area {
  flex: 1;
  overflow-y: auto;
  padding: 20px 24px;
  max-width: 1200px;
  width: 100%;
  margin: 0 auto;
  box-sizing: border-box;
}

.panel {
  background: #fff;
  border-radius: 8px;
  border: 1px solid #e8e8e8;
  overflow: hidden;
  box-shadow: 0 1px 4px rgba(0,0,0,.04);
}

/* 信息栏（订阅/分组头部） */
.info-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 16px;
  background: #fafafa;
  border-bottom: 1px solid #f0f0f0;
  gap: 12px;
}
.info-bar-left {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
  flex: 1;
}
.info-bar-right { display: flex; gap: 6px; flex-shrink: 0; }
.info-url {
  font-size: 12px;
  color: #595959;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.info-time { font-size: 11px; color: #bfbfbf; white-space: nowrap; }
.info-count { font-size: 12px; color: #8c8c8c; }
.group-name-display { font-size: 14px; font-weight: 600; color: #262626; }

/* 空状态 */
.empty-hint {
  text-align: center;
  padding: 60px 20px;
  color: #8c8c8c;
}
.empty-icon { font-size: 40px; margin-bottom: 12px; }
.empty-title { font-size: 15px; font-weight: 500; color: #595959; margin-bottom: 6px; }
.empty-sub { font-size: 13px; color: #bfbfbf; }

/* 添加节点选择列表 */
.sel-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 10px 20px;
  cursor: pointer;
  border-bottom: 1px solid #f5f5f5;
  transition: background .1s;
}
.sel-row:last-child { border-bottom: none; }
.sel-row:hover { background: #f5f5f5; }
.sel-row-active { background: #e6f0ff; }
.sel-name { flex: 1; font-size: 13px; color: #262626; }
.sel-addr { font-size: 11px; color: #bfbfbf; }
</style>
