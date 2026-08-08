<template>
  <div class="home" :style="columnVars">
    <!-- Tab 栏 -->
    <div class="tab-bar-outer">
      <div class="tab-bar">
        <button
          v-if="tabsOverflow"
          class="tab-scroll-btn"
          :disabled="!canScrollLeft"
          title="向左滚动（双击到最左）"
          @click="scrollTabs(-1)"
          @dblclick="scrollTabsToEdge(-1)"
        >‹</button>
        <div
          :class="['tabs-viewport', canScrollLeft ? 'has-left' : '', canScrollRight ? 'has-right' : '']"
        >
          <div
            class="tabs-wrap"
            ref="tabsWrapEl"
            @wheel.prevent="onTabsWheel"
            @scroll.passive="updateTabScrollState"
          >
            <span
              v-for="(group, gi) in manualGroups" :key="'group-' + group.id"
              :data-tab-key="'group-' + gi"
              :class="['tab', activeTab === 'group-' + gi ? 'tab-active' : '']"
              @click="activeTab = 'group-' + gi"
            >
              {{ group.name }}
              <span class="tab-badge">{{ group.servers?.length || 0 }}</span>
            </span>
            <span
              v-for="(sub, si) in store.subscriptions" :key="'sub-' + sub.id"
              :data-tab-key="'sub-' + si"
              :class="['tab', activeTab === 'sub-' + si ? 'tab-active' : '']"
              @click="activeTab = 'sub-' + si"
            >
              {{ sub.name || sub.host }}
              <span class="tab-badge">{{ sub.servers?.length || 0 }}</span>
            </span>
          </div>
        </div>
        <button
          v-if="tabsOverflow"
          class="tab-scroll-btn"
          :disabled="!canScrollRight"
          title="向右滚动（双击到最右）"
          @click="scrollTabs(1)"
          @dblclick="scrollTabsToEdge(1)"
        >›</button>
        <div class="tab-actions">
          <button class="action-btn action-btn-primary" @click="openImport">＋ 导入</button>
          <button class="action-btn" @click="showNewGroup = true">📁 新建分组</button>
        </div>
      </div>
    </div>

    <!-- 当前出站状态条已移至 App.vue 全局显示 -->
    <div class="content-area">

      <div v-if="notice" :class="['notice-bar', { 'notice-bar-error': notice.error }]">
        <span>{{ notice.text }}</span>
        <button class="notice-close" @click="notice = null" title="关闭">✕</button>
      </div>

      <!-- 多选工具栏（有选中时显示） -->
      <div class="bulk-bar" v-if="selectedRefs.length || showSelectAll">
        <span class="bulk-count">已选 {{ selectedRefs.length }} 个节点</span>
        <button class="action-btn" @click="selectAll" v-if="!allSelected">☑ 全选</button>
        <button class="action-btn" @click="selectedRefs = []" v-if="allSelected">☐ 取消全选</button>
        <button class="action-btn" @click="pingSelected('fast')" :disabled="pinging">⚡ 批量快速</button>
        <button class="action-btn" @click="pingSelected('real')" :disabled="pinging">🧪 批量真连</button>
        <button v-if="pinging" class="action-btn action-btn-danger" @click="abortPing">⏹ 中止检测</button>
        <span v-if="pinging && pingProgress.total > 0" class="ping-progress">{{ pingProgress.done }}/{{ pingProgress.total }}</span>
        <button class="action-btn" @click="detectIPTypeForRefs(selectedRefs)" :disabled="detectingIPType">
          🌐 {{ detectingIPType ? '检测中…' : '检测 IP 类型' }}
        </button>
        <button class="action-btn" @click="scoreIPForRefs(selectedRefs)" :disabled="ipScoreTask.running" title="查询 IP 纯净度评分（后台串行执行）">
          🧮 {{ ipScoreTask.running ? `评分中 ${ipScoreTask.done}/${ipScoreTask.total}` : '查 IP 评分' }}
        </button>
        <button v-if="ipScoreTask.running" class="action-btn action-btn-danger" @click="abortIPScore">⏹ 中止评分</button>
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
          <span class="info-count">{{ sortedSearchResults.length }} 个节点</span>
          <button class="action-btn" style="margin-left:auto" @click="searchQuery=''; isSearching=false">✕ 关闭</button>
        </div>
        <div v-if="!sortedSearchResults.length" class="empty-hint" style="padding:20px 0">
          <p class="empty-sub">无匹配节点</p>
        </div>
        <div class="table-scroll" v-else>
          <ServerListHead
            :columns="listColumns"
            :sort-key="sortKey"
            :sort-dir="sortDir"
            @sort="onSortColumn"
            @resize="onResizeColumn"
            @resize-end="onResizeColumnEnd"
            @reset-width="onResetColumnWidth"
          />
          <ServerRow
            v-for="(r, i) in sortedSearchResults" :key="refKey(r.ref) + ':' + i"
            :server="r.server" :ref-obj="r.ref" :outbounds="store.outbounds" :removable="false"
            :current-outbound="currentOutbound"
            :ping-state="pingStates[refKey(r.ref)] || null"
            :ip-type-state="ipTypeStates[refKey(r.ref)] || null"
            :ip-score-running="!!ipScoringRefs[refKey(r.ref)]"
            :selected="isSelected(r.ref)"
            @connect="connectNode"
            @select="toggleSelect"
            @select-start="startDragSelect"
            @select-enter="continueDragSelect"
            @share="(ref, sv) => openNodeMenu('share', ref, sv)"
          />
        </div>
      </div>

      <!-- 订阅 tab -->
      <template v-for="(sub, si) in store.subscriptions" :key="'sub-' + sub.id">
        <div v-if="activeTab === 'sub-' + si" class="panel panel-fill">
          <div class="info-bar">
            <div class="info-bar-left">
              <span class="info-url" :title="sub.url">{{ sub.url }}</span>
              <span v-if="sub.updatedAt" class="info-time">{{ formatTime(sub.updatedAt) }}</span>
            </div>
            <div class="info-bar-right">
              <input
                class="search-input"
                v-model="searchQuery"
                placeholder="搜索节点…"
                @input="onSearch"
              />
              <button class="action-btn" @click="refreshPanel" :disabled="refreshing" title="重新加载节点数据">
                ↻ {{ refreshing ? '刷新中…' : '刷新' }}
              </button>
              <button class="action-btn" @click="pingCurrentTab('fast')" :disabled="pinging">
                ⚡ {{ pinging ? '探测中…' : '快速检测' }}
              </button>
              <button class="action-btn" @click="pingCurrentTab('real')" :disabled="pinging">
                🧪 {{ pinging ? '探测中…' : '真连接检测' }}
              </button>
              <button v-if="pinging" class="action-btn action-btn-danger" @click="abortPing">⏹ 中止检测</button>
              <span v-if="pinging && pingProgress.total > 0" class="ping-progress">{{ pingProgress.done }}/{{ pingProgress.total }}</span>
              <button class="action-btn" @click="detectIPTypeForCurrentTab" :disabled="detectingIPType" title="查询节点地址是机房还是家宽">
                🌐 {{ detectingIPType ? '检测中…' : 'IP 类型' }}
              </button>
              <button class="action-btn" @click="scoreIPForCurrentTab" :disabled="ipScoreTask.running" title="查询 IP 纯净度评分（后台串行执行，避免触发第三方限流）">
                🧮 {{ ipScoreTask.running ? `评分中 ${ipScoreTask.done}/${ipScoreTask.total}` : 'IP 评分' }}
              </button>
              <button v-if="ipScoreTask.running" class="action-btn action-btn-danger" @click="abortIPScore">⏹ 中止评分</button>
              <button class="action-btn" @click="refreshSub(si)" title="重新拉取订阅节点">↻ 更新节点</button>
              <button class="action-btn" @click="openEditSub(si)" title="编辑订阅名称和链接">✏ 编辑</button>
              <button class="action-btn action-btn-danger" @click="deleteSub(si)">删除</button>
            </div>
          </div>
          <div v-if="!sub.servers?.length" class="empty-hint">
            <p class="empty-sub">订阅为空，点击「更新节点」拉取</p>
          </div>
          <div class="table-scroll" v-if="getSortedSubEntries(sub, si).length">
            <ServerListHead
              :columns="listColumns"
              :sort-key="sortKey"
              :sort-dir="sortDir"
              @sort="onSortColumn"
              @resize="onResizeColumn"
              @resize-end="onResizeColumnEnd"
              @reset-width="onResetColumnWidth"
            />
            <ServerRow
              v-for="(item, i) in getSortedSubEntries(sub, si)" :key="refKey(item.ref) + ':' + i"
              :server="item.server" :ref-obj="item.ref"
              :outbounds="store.outbounds" :removable="false"
              :current-outbound="currentOutbound"
              :ping-state="pingStates[refKey(item.ref)] || null"
              :ip-type-state="ipTypeStates[refKey(item.ref)] || null"
              :ip-score-running="!!ipScoringRefs[refKey(item.ref)]"
              :selected="isSelected(item.ref)"
              @connect="connectNode"
              @select="toggleSelect"
              @select-start="startDragSelect"
              @select-enter="continueDragSelect"
              @share="(r, sv) => openNodeMenu('share', r, sv)"
              @copy-to-group="ref => openSingleCopy(ref)"
            />
          </div>
        </div>
      </template>

      <!-- 手动分组 tab -->
      <template v-for="(group, gi) in manualGroups" :key="'group-' + group.id">
        <div v-if="activeTab === 'group-' + gi" class="panel panel-fill">
          <div class="info-bar">
            <div class="info-bar-left">
              <span class="group-name-display">{{ group.name }}</span>
              <span class="info-count">{{ getSortedGroupEntries(group).length }} 个节点</span>
            </div>
            <div class="info-bar-right">
              <input
                class="search-input"
                v-model="searchQuery"
                placeholder="搜索节点…"
                @input="onSearch"
              />
              <button class="action-btn" @click="refreshPanel" :disabled="refreshing" title="重新加载节点数据">
                ↻ {{ refreshing ? '刷新中…' : '刷新' }}
              </button>
              <button class="action-btn" @click="pingCurrentTab('fast')" :disabled="pinging">
                ⚡ {{ pinging ? '探测中…' : '快速检测' }}
              </button>
              <button class="action-btn" @click="pingCurrentTab('real')" :disabled="pinging">
                🧪 {{ pinging ? '探测中…' : '真连接检测' }}
              </button>
              <button v-if="pinging" class="action-btn action-btn-danger" @click="abortPing">⏹ 中止检测</button>
              <span v-if="pinging && pingProgress.total > 0" class="ping-progress">{{ pingProgress.done }}/{{ pingProgress.total }}</span>
              <button class="action-btn" @click="detectIPTypeForCurrentTab" :disabled="detectingIPType" title="查询节点地址是机房还是家宽">
                🌐 {{ detectingIPType ? '检测中…' : 'IP 类型' }}
              </button>
              <button class="action-btn" @click="scoreIPForCurrentTab" :disabled="ipScoreTask.running" title="查询 IP 纯净度评分（后台串行执行，避免触发第三方限流）">
                🧮 {{ ipScoreTask.running ? `评分中 ${ipScoreTask.done}/${ipScoreTask.total}` : 'IP 评分' }}
              </button>
              <button v-if="ipScoreTask.running" class="action-btn action-btn-danger" @click="abortIPScore">⏹ 中止评分</button>
              <button v-if="group.name !== 'SERVER'" class="action-btn" @click="openEditGroup(gi)">✏ 编辑</button>
              <button class="action-btn" @click="openAddToGroup(gi)">＋ 添加节点</button>
              <button v-if="group.name !== 'SERVER'" class="action-btn action-btn-danger" @click="deleteGroup(gi)">删除分组</button>
            </div>
          </div>
          <div v-if="!getSortedGroupEntries(group).length" class="empty-hint">
            <p class="empty-sub">分组为空，点击「添加节点」</p>
          </div>
          <div class="table-scroll" v-if="getSortedGroupEntries(group).length">
            <ServerListHead
              :columns="listColumns"
              :sort-key="sortKey"
              :sort-dir="sortDir"
              @sort="onSortColumn"
              @resize="onResizeColumn"
              @resize-end="onResizeColumnEnd"
              @reset-width="onResetColumnWidth"
            />
            <ServerRow
              v-for="(item, i) in getSortedGroupEntries(group)" :key="refKey(item.ref) + ':' + i"
              :server="item.server" :ref-obj="item.ref" :outbounds="store.outbounds" :removable="true"
              :current-outbound="currentOutbound"
              :ping-state="pingStates[refKey(item.ref)] || null"
              :ip-type-state="ipTypeStates[refKey(item.ref)] || null"
              :ip-score-running="!!ipScoringRefs[refKey(item.ref)]"
              :selected="isSelected(item.ref)"
              @connect="connectNode"
              @select="toggleSelect"
              @select-start="startDragSelect"
              @select-enter="continueDragSelect"
              @remove="ref => removeFromGroup(realGroupIndex(group), ref)"
              @edit="(r, sv) => openNodeMenu('edit', r, sv)"
              @share="(r, sv) => openNodeMenu('share', r, sv)"
              @copy-to-group="ref => openSingleCopy(ref)"
              @move-to-group="ref => openSingleMove(ref)"
            />
          </div>
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
    <div class="modal-overlay" v-if="showNewGroup" @mousedown.self="showNewGroup = false">
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
    <div class="modal-overlay" v-if="editGroupIdx >= 0" @mousedown.self="editGroupIdx = -1">
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
    <div class="modal-overlay" v-if="showAddToGroup >= 0" @mousedown.self="showAddToGroup = -1">
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
    <div class="modal-overlay" v-if="editSubIdx >= 0" @mousedown.self="editSubIdx = -1">
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
          <div class="form-group">
            <label class="form-label">更新方式</label>
            <select class="input" v-model="editSubForm.proxyOutbound">
              <option value="">直连更新</option>
              <option v-for="ob in store.outbounds" :key="ob.name" :value="ob.name">
                通过代理更新（{{ ob.name }}）
              </option>
            </select>
            <div class="form-hint">
              订阅地址需要代理才能访问时选择「通过代理更新」，需内核处于运行状态。
              该订阅下的节点会自动以此出站作为前置代理（节点单独设置的前置代理优先）。
            </div>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" @click="editSubIdx = -1">取消</button>
          <button class="btn btn-primary" @click="saveEditSub">保存</button>
        </div>
      </div>
    </div>

    <!-- 批量复制到分组弹窗 -->
    <div class="modal-overlay" v-if="showBulkCopyModal" @mousedown.self="showBulkCopyModal = false">
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
    <div class="modal-overlay" v-if="showBulkMoveModal" @mousedown.self="showBulkMoveModal = false">
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
    <div class="modal-overlay" v-if="showSingleCopyModal" @mousedown.self="showSingleCopyModal = false">
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
    <div class="modal-overlay" v-if="showSingleMoveModal" @mousedown.self="showSingleMoveModal = false">
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
import { ref, computed, inject, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { useProxyStore } from '../stores/proxy'
import { api } from '../api'
import ServerRow from '../components/ServerRow.vue'
import ServerListHead from '../components/ServerListHead.vue'
import NodeMenu from '../components/NodeMenu.vue'
import axios from 'axios'

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
const selectableServers = ref([])
const searchQuery = ref('')
const searchResults = ref([])
const isSearching = ref(false)
const pingProgress = ref({ total: 0, done: 0, current: '' })
const pingStates = ref({})
const FAST_PING_CONCURRENCY = 6

// ---- 列宽 ----
// 操作列也是一列：6 个按钮实际要 220px 左右，之前固定 180px 且右对齐 + 裁剪，
// 导致最左侧的按钮被切掉。放进来后既有足够默认宽度，也能像其他列一样拖动调整。
const COLUMN_DEFS = [
  { key: 'name', label: '节点', min: 80, def: 200, sortable: true },
  { key: 'addr', label: '地址', min: 90, def: 220, sortable: true },
  { key: 'type', label: '协议', min: 50, def: 76, sortable: true },
  { key: 'iptype', label: 'IP 类型', min: 46, def: 62, sortable: true },
  { key: 'ipscore', label: '评分', min: 46, def: 62, sortable: true },
  { key: 'latency', label: '延迟', min: 46, def: 64, sortable: true },
  { key: 'ops', label: '操作', min: 120, def: 232, noResizer: true },
]
const COLUMN_WIDTH_STORAGE_KEY = 'ps.serverList.columnWidths'

const columnWidths = ref(loadColumnWidths())

function loadColumnWidths() {
  const widths = {}
  for (const col of COLUMN_DEFS) widths[col.key] = col.def
  try {
    const saved = JSON.parse(localStorage.getItem(COLUMN_WIDTH_STORAGE_KEY) || '{}')
    for (const col of COLUMN_DEFS) {
      const v = Number(saved[col.key])
      if (Number.isFinite(v) && v >= col.min) widths[col.key] = v
    }
  } catch {}
  return widths
}

function persistColumnWidths() {
  try {
    localStorage.setItem(COLUMN_WIDTH_STORAGE_KEY, JSON.stringify(columnWidths.value))
  } catch {}
}

const listColumns = computed(() => COLUMN_DEFS.map(col => ({
  key: col.key,
  label: col.label,
  sortable: !!col.sortable,
  noResizer: !!col.noResizer,
})))

const columnVars = computed(() => {
  const vars = {}
  for (const col of COLUMN_DEFS) vars[`--col-${col.key}`] = `${columnWidths.value[col.key]}px`
  return vars
})

// 拖动过程中每次都以按下时的宽度为基准，避免累积误差。
// 基准在一次拖动结束时清空，否则同一列再次拖动会从上一轮的起点算起、宽度突跳。
let resizeBase = null
function onResizeColumn(key, delta) {
  const def = COLUMN_DEFS.find(c => c.key === key)
  if (!def) return
  if (!resizeBase || resizeBase.key !== key) {
    resizeBase = { key, width: columnWidths.value[key] }
  }
  const next = Math.max(def.min, Math.round(resizeBase.width + delta))
  columnWidths.value = { ...columnWidths.value, [key]: next }
  persistColumnWidths()
}

function onResizeColumnEnd() {
  resizeBase = null
}

function onResetColumnWidth(key) {
  const def = COLUMN_DEFS.find(c => c.key === key)
  if (!def) return
  resizeBase = null
  columnWidths.value = { ...columnWidths.value, [key]: def.def }
  persistColumnWidths()
}

// ---- 排序 ----
// 排序结果是点击表头那一刻的快照：把每个节点的名次固定下来，之后延迟再怎么变都
// 只更新数字、不重新排位，避免测速过程中行来回跳导致点错节点。
const sortKey = ref('')
const sortDir = ref('') // 'asc' | 'desc' | ''

function onSortColumn(key) {
  if (sortKey.value === key) {
    // 同一列：升序 → 降序 → 还原
    if (sortDir.value === 'asc') {
      sortDir.value = 'desc'
    } else if (sortDir.value === 'desc') {
      sortDir.value = ''
      sortKey.value = ''
    }
  } else {
    // 不同列：从升序开始
    sortKey.value = key
    sortDir.value = 'asc'
  }
  captureSortOrder()
}

const sortRanks = ref(null)

function sortValueFor(key, server) {
  if (key === 'latency') return latencyValue(server)
  if (key === 'iptype') return ipTypeOrder(server?.ipType)
  // 评分越高越好，取负值让"升序"= 从优到劣，和延迟列的方向观感一致
  if (key === 'ipscore') {
    const s = server?.ipScore
    return (s === null || s === undefined) ? Number.MAX_SAFE_INTEGER : -s
  }
  return 0
}

// numeric 让"节点2"排在"节点10"前面，也让 IP 段按数值而不是字典序比
const textCollator = new Intl.Collator('zh-Hans-CN', { numeric: true, sensitivity: 'base' })

// 空值一律沉底，和"未检测"列的处理保持一致
function compareText(a, b) {
  const x = (a || '').trim()
  const y = (b || '').trim()
  if (!x || !y) return (x ? 0 : 1) - (y ? 0 : 1)
  return textCollator.compare(x, y)
}

function compareServers(key, a, b) {
  if (key === 'name') return compareText(a?.name, b?.name)
  if (key === 'type') return compareText(a?.type, b?.type)
  if (key === 'addr') {
    return compareText(a?.host, b?.host) || (Number(a?.port) || 0) - (Number(b?.port) || 0)
  }
  return sortValueFor(key, a) - sortValueFor(key, b)
}

// 未检测的排在最后，避免空值抢占前排
function ipTypeOrder(type) {
  const order = { idc: 0, residential: 1, mobile: 2, unknown: 3 }
  const v = order[type]
  return v === undefined ? Number.MAX_SAFE_INTEGER : v
}

function latencyValue(server) {
  const lat = Number(server?.latency)
  // -1=未测速, 0=超时，都排在最后
  if (!Number.isFinite(lat) || lat < 0) return Number.MAX_SAFE_INTEGER
  if (lat === 0) return Number.MAX_SAFE_INTEGER - 1
  return lat
}

// 对所有节点（手动 + 全部订阅）统一算名次，这样切换 tab 时排序依然生效。
// 名次按节点身份存储而非下标：删节点或刷新订阅会让下标整体前移，用下标当键会把
// 名次张冠李戴到别的节点上。
function captureSortOrder() {
  if (!sortKey.value) {
    sortRanks.value = null
    return
  }
  const items = []
  store.servers.forEach((server) => items.push(server))
  store.subscriptions.forEach((sub) => {
    (sub?.servers || []).forEach((server) => items.push(server))
  })

  const dir = sortDir.value === 'desc' ? -1 : 1
  items.sort((a, b) => compareServers(sortKey.value, a, b) * dir)

  const ranks = new Map()
  items.forEach((server, i) => {
    const id = nodeIdentity(server)
    if (!ranks.has(id)) ranks.set(id, i)
  })
  sortRanks.value = ranks
}

// 与后端 serverProbeKeys 同样的思路：优先用链接，退回协议/地址/端口/名称组合
function nodeIdentity(server) {
  const link = (server?.link || '').trim()
  if (link) return `link:${link}`
  const type = (server?.type || '').toLowerCase().trim()
  const host = (server?.host || '').toLowerCase().trim()
  return `srv:${type}|${host}|${server?.port || 0}|${(server?.name || '').trim()}`
}

// 按点击表头时固定下来的名次排列。新导入、排序后才出现的节点没有名次，排在末尾。
function applySortOrder(entries) {
  const ranks = sortRanks.value
  if (!ranks) return entries
  return [...entries].sort((a, b) => {
    const ra = ranks.get(nodeIdentity(a.server))
    const rb = ranks.get(nodeIdentity(b.server))
    if (ra === undefined && rb === undefined) return 0
    if (ra === undefined) return 1
    if (rb === undefined) return -1
    return ra - rb
  })
}
const pingAbortController = ref(null)
const pingAborted = ref(false)

// IP 类型检测状态
const detectingIPType = ref(false)
const ipTypeStates = ref({})

// IP 评分：后端串行限速跑，这里轮询进度
const ipScoreTask = ref({ running: false, total: 0, done: 0, failed: 0, currentIp: '', lastError: '' })

// 顶部轻提示：后台任务没有即时结果，至少要让用户知道"已经开始了"
const notice = ref(null)
let noticeTimer = null
function showNotice(text, isError = false) {
  notice.value = { text, error: isError }
  if (noticeTimer) clearTimeout(noticeTimer)
  noticeTimer = setTimeout(() => { notice.value = null }, isError ? 8000 : 5000)
}

const refreshing = ref(false)
async function refreshPanel() {
  if (refreshing.value) return
  refreshing.value = true
  try {
    await store.fetchAll()
  } catch (e) {
    showNotice('刷新失败：' + (e?.response?.data?.error || e?.message || '未知错误'), true)
  } finally {
    refreshing.value = false
  }
}
const ipScoringRefs = ref({})
let ipScorePollTimer = null

// 订阅编辑状态
const editSubIdx = ref(-1)
const editSubForm = ref({ name: '', url: '', format: 'auto', proxyOutbound: '' })

// 批量复制到分组
const showBulkCopyModal = ref(false)
const bulkCopyGroupId = ref('')

// NodeMenu 状态
const nodeMenuMode = ref('')
const nodeMenuRef = ref(null)
const nodeMenuServer = ref(null)

// 多选状态
const selectedRefs = ref([])
const selectionAnchorKey = ref('')
const dragSelecting = ref(false)
const dragSelectChecked = ref(true)
const dragLastKey = ref('')
const dragStartPos = ref({ x: 0, y: 0 }) // 记录拖拽开始位置
const dragMoved = ref(false) // 标记是否真正发生了拖拽移动

// ===== Tab 栏横向滚动 =====
const tabsWrapEl = ref(null)
const tabsOverflow = ref(false)
const canScrollLeft = ref(false)
const canScrollRight = ref(false)
let tabsResizeObserver = null

function updateTabScrollState() {
  const el = tabsWrapEl.value
  if (!el) return
  // 亚像素误差会让「已经到底」被误判成还能滚，留 1px 容差
  const maxScroll = el.scrollWidth - el.clientWidth
  tabsOverflow.value = maxScroll > 1
  canScrollLeft.value = el.scrollLeft > 1
  canScrollRight.value = el.scrollLeft < maxScroll - 1
}

function scrollTabs(dir) {
  const el = tabsWrapEl.value
  if (!el) return
  el.scrollBy({ left: dir * el.clientWidth * 0.8, behavior: 'smooth' })
}

function scrollTabsToEdge(dir) {
  const el = tabsWrapEl.value
  if (!el) return
  el.scrollTo({ left: dir < 0 ? 0 : el.scrollWidth, behavior: 'smooth' })
}

// 竖向滚轮转成横向滚动，触控板的横向分量优先
function onTabsWheel(e) {
  const el = tabsWrapEl.value
  if (!el) return
  el.scrollLeft += Math.abs(e.deltaX) > Math.abs(e.deltaY) ? e.deltaX : e.deltaY
}

function scrollActiveTabIntoView() {
  const el = tabsWrapEl.value
  if (!el) return
  const target = el.querySelector(`[data-tab-key="${activeTab.value}"]`)
  target?.scrollIntoView({ behavior: 'smooth', block: 'nearest', inline: 'nearest' })
}

onMounted(() => {
  updateTabScrollState()
  if (typeof ResizeObserver !== 'undefined' && tabsWrapEl.value) {
    tabsResizeObserver = new ResizeObserver(updateTabScrollState)
    tabsResizeObserver.observe(tabsWrapEl.value)
  }
  window.addEventListener('resize', updateTabScrollState)
  window.addEventListener('pointerup', endDragSelect)
  window.addEventListener('pointercancel', endDragSelect)
  window.addEventListener('blur', endDragSelect)
  // 评分任务跑在后端，刷新页面后要接回进度显示
  resumeIPScoreIfRunning()
})

onBeforeUnmount(() => {
  tabsResizeObserver?.disconnect()
  window.removeEventListener('resize', updateTabScrollState)
  window.removeEventListener('pointerup', endDragSelect)
  window.removeEventListener('pointercancel', endDragSelect)
  window.removeEventListener('blur', endDragSelect)
  stopIPScorePoll()
})

async function resumeIPScoreIfRunning() {
  try {
    const { data } = await api.getIPScoreStatus()
    if (data?.running) startIPScorePoll()
  } catch {}
}

function refKey(ref) {
  return `${ref.type}:${ref.index}:${ref.sub || 0}`
}
function isSelected(ref) {
  return selectedRefs.value.some(r => refKey(r) === refKey(ref))
}

function setSelected(ref, checked) {
  if (checked) {
    if (!isSelected(ref)) selectedRefs.value = [...selectedRefs.value, ref]
    return
  }
  selectedRefs.value = selectedRefs.value.filter(r => refKey(r) !== refKey(ref))
}

function getVisibleRefs() {
  if (isSearching.value) return sortedSearchResults.value.map(entry => entry.ref)
  return getSortedCurrentTabRefs()
}

function selectRange(fromKey, toRef, checked) {
  const visibleRefs = getVisibleRefs()
  const fromIndex = visibleRefs.findIndex(ref => refKey(ref) === fromKey)
  const toIndex = visibleRefs.findIndex(ref => refKey(ref) === refKey(toRef))
  if (fromIndex < 0 || toIndex < 0) {
    setSelected(toRef, checked)
    return
  }

  const start = Math.min(fromIndex, toIndex)
  const end = Math.max(fromIndex, toIndex)
  const rangeRefs = visibleRefs.slice(start, end + 1)
  const rangeKeys = new Set(rangeRefs.map(refKey))

  if (checked) {
    const selectedKeys = new Set(selectedRefs.value.map(refKey))
    selectedRefs.value = [
      ...selectedRefs.value,
      ...rangeRefs.filter(ref => !selectedKeys.has(refKey(ref))),
    ]
    return
  }
  selectedRefs.value = selectedRefs.value.filter(ref => !rangeKeys.has(refKey(ref)))
}

function toggleSelect(ref, checked, event) {
  if (event?.shiftKey && selectionAnchorKey.value) {
    selectRange(selectionAnchorKey.value, ref, checked)
    return
  }
  setSelected(ref, checked)
  selectionAnchorKey.value = refKey(ref)
}

function startDragSelect(ref, event) {
  if (event.button !== 0) return

  const checked = !isSelected(ref)
  if (event.shiftKey && selectionAnchorKey.value) {
    selectRange(selectionAnchorKey.value, ref, checked)
  } else {
    setSelected(ref, checked)
    selectionAnchorKey.value = refKey(ref)
  }

  dragSelecting.value = true
  dragSelectChecked.value = checked
  dragLastKey.value = refKey(ref)
  dragStartPos.value = { x: event.clientX, y: event.clientY }
  dragMoved.value = false
  // 单元格文字现在可以划词，拖拽多选时要临时关掉，否则会顺带选中整片文本
  document.body.style.userSelect = 'none'
}

function continueDragSelect(ref, event) {
  if (!dragSelecting.value || !(event.buttons & 1)) return

  // 检查是否真正移动了（移动超过 5 像素才算拖拽）
  if (!dragMoved.value) {
    const dx = Math.abs(event.clientX - dragStartPos.value.x)
    const dy = Math.abs(event.clientY - dragStartPos.value.y)
    if (dx < 5 && dy < 5) return // 移动距离太小，不算拖拽
    dragMoved.value = true
  }

  const currentKey = refKey(ref)
  if (currentKey === dragLastKey.value) return
  selectRange(dragLastKey.value, ref, dragSelectChecked.value)
  dragLastKey.value = currentKey
}

function endDragSelect() {
  if (dragSelecting.value) document.body.style.userSelect = ''
  dragSelecting.value = false
  dragLastKey.value = ''
  dragMoved.value = false
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

function getSortedCurrentTabRefs() {
  if (activeTab.value.startsWith('sub-')) {
    const si = parseInt(activeTab.value.replace('sub-', ''))
    const sub = store.subscriptions[si]
    if (!sub?.servers) return []
    return getSortedSubEntries(sub, si).map(e => e.ref)
  }
  if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    if (!group?.servers) return []
    return getSortedGroupEntries(group).map(e => e.ref)
  }
  return []
}



function selectAll() {
  const currentTabServers = getCurrentTabServers()
  selectedRefs.value = [...currentTabServers]
  showSelectAll.value = true
}

// 切换 tab 时清空选择
watch(activeTab, async () => {
  selectedRefs.value = []
  showSelectAll.value = false
  selectionAnchorKey.value = ''
  endDragSelect()
  await nextTick()
  scrollActiveTabIntoView()
})

watch(
	() => [currentOutbound?.value, manualGroups.value.map(g => g.id).join(','), store.outbounds],
	() => {
		const outboundName = currentOutbound?.value || 'proxy'
		const outbound = store.outbounds.find(o => o.name === outboundName)
    const target = outbound?.target
    if (!target?.targetType) return

    if (target.targetType === 'group' && target.groupId) {
      const gi = manualGroups.value.findIndex(g => g.id === target.groupId)
      if (gi >= 0) activeTab.value = 'group-' + gi
      return
    }

    if (target.targetType === 'node' && target.nodeRef) {
      const ref = target.nodeRef
      if (ref.type === 'sub_server') {
        const si = Number(ref.sub || 0)
        if (si >= 0 && si < store.subscriptions.length) {
          activeTab.value = 'sub-' + si
        }
        return
      }
      if (ref.type === 'server') {
        const gi = manualGroups.value.findIndex(g =>
          (g.servers || []).some(r => r.type === 'server' && r.index === ref.index)
        )
        if (gi >= 0) activeTab.value = 'group-' + gi
      }
    }
	},
	{ immediate: true }
)

// 当前 tab 是否可批量删除（只有手动分组可删）
const canBulkRemove = computed(() => {
  return activeTab.value.startsWith('group-')
})

// 一次性把所有 refs 提交给后端，由后端 worker pool 按配置的并发数调度
// （real 默认 4、fast 16），前端只消费 SSE 进度事件更新状态。
async function pingRefsWithProgress(refs, mode = 'fast') {
  pingProgress.value = { total: refs.length, done: 0, current: '' }
  pingAborted.value = false
  pingAbortController.value = new AbortController()

  const queue = refs.map(ref => ({ type: ref.type, index: ref.index, sub: ref.sub || 0 }))
  const stateMap = {}
  for (const ref of queue) {
    stateMap[refKey(ref)] = { status: 'pending' }
  }
  pingStates.value = stateMap

  try {
    await api.pingNodes(queue, mode, {
      signal: pingAbortController.value.signal,
      onEvent: (evt, payload) => {
        const ref = payload?.ref
        if (!ref) return
        const key = refKey({ type: ref.type, index: ref.index, sub: ref.sub || 0 })
        if (evt === 'running') {
          pingStates.value[key] = { status: 'running' }
        } else if (evt === 'done') {
          const lat = payload.latency
          pingStates.value[key] = { status: 'done', latency: typeof lat === 'number' ? lat : -1 }
          pingProgress.value.done += 1
        }
      },
    })
  } catch (e) {
    if (e?.name === 'AbortError' || pingAbortController.value?.signal?.aborted) {
      pingAborted.value = true
      return
    }
    throw e
  }
}

function abortPing() {
  if (pingAbortController.value) {
    pingAbortController.value.abort()
    pingAborted.value = true
  }
}

async function pingSelected(mode = 'fast') {
  if (!selectedRefs.value.length) return
  pinging.value = true
  try {
    await pingRefsWithProgress(selectedRefs.value, mode)
    await store.fetchAll()
  } finally {
    pingAbortController.value = null
    pingProgress.value = { total: 0, done: 0, current: '' }
    pingStates.value = {}
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

// tab 数量变化后要重算溢出状态（须在 manualGroups 定义之后注册，watch 的 getter 会立即求值）
watch(
  () => [manualGroups.value.length, store.subscriptions.length],
  async () => {
    await nextTick()
    updateTabScrollState()
  }
)

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

function getGroupServerEntries(group) {
  return (group.servers || []).map(ref => {
    if (ref.type === 'server') return { ref, server: store.servers[ref.index] }
    const sub = store.subscriptions[ref.sub]
    return { ref, server: sub?.servers?.[ref.index] }
  }).filter(item => item.server)
}

function getSortedGroupEntries(group) {
  return applySortOrder(getGroupServerEntries(group))
}

function getSubServerEntries(sub, si) {
  return (sub?.servers || []).map((server, index) => ({
    ref: { type: 'sub_server', index, sub: si },
    server,
  }))
}

function getSortedSubEntries(sub, si) {
  return applySortOrder(getSubServerEntries(sub, si))
}

const sortedSearchResults = computed(() => applySortOrder(searchResults.value))

const filteredSelectableServers = computed(() => {
  if (!addSearch.value.trim()) return selectableServers.value
  const q = addSearch.value.toLowerCase()
  return selectableServers.value.filter(s =>
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
  await api.pingNodes([{ type: ref.type, index: ref.index, sub: ref.sub || 0 }], 'fast')
  await store.fetchAll()
}

async function pingCurrentTab(mode = 'fast') {
  pinging.value = true
  try {
    let refs = []
    let group = null
    if (activeTab.value.startsWith('sub-')) {
      const si = parseInt(activeTab.value.replace('sub-', ''))
      const sub = store.subscriptions[si]
      if (sub?.servers?.length) {
        refs = sub.servers.map((_, i) => ({ type: 'sub_server', index: i, sub: si }))
      }
      group = store.groups.find(g => g.id === sub?.groupId) || null
    } else if (activeTab.value.startsWith('group-')) {
      const gi = parseInt(activeTab.value.replace('group-', ''))
      group = manualGroups.value[gi] || null
      if (group) refs = (group.servers || []).map(r => ({ type: r.type, index: r.index, sub: r.sub || 0 }))
    }
    if (refs.length) await pingRefsWithProgress(refs, mode)
    // 中途中断时数据只有一半，用它判定切换会挑到"恰好先测完"的节点
    if (refs.length && group && !pingAborted.value) {
      await applyGroupSwitchAfterPing(group, mode)
    }
    await store.fetchAll()
  } finally {
    pingAbortController.value = null
    pingProgress.value = { total: 0, done: 0, current: '' }
    pingStates.value = {}
    pinging.value = false
  }
}

// 分组测速跑完后交给后端按绑定策略决定是否切换节点。后端会跳过测速方式与本次不一致
// 的出站，切换结果通过日志面板反馈。
async function applyGroupSwitchAfterPing(group, mode) {
  const index = realGroupIndex(group)
  if (index < 0) return
  try {
    await api.applyGroupSwitch(index, mode)
  } catch (e) {
    console.error('应用分组切换策略失败', e)
  }
}

// 检测当前 tab 全部节点的 IP 类型
async function detectIPTypeForCurrentTab() {
  let refs = []
  if (activeTab.value.startsWith('sub-')) {
    const si = parseInt(activeTab.value.replace('sub-', ''))
    const sub = store.subscriptions[si]
    refs = (sub?.servers || []).map((_, i) => ({ type: 'sub_server', index: i, sub: si }))
  } else if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    refs = (group?.servers || []).map(r => ({ type: r.type, index: r.index, sub: r.sub || 0 }))
  }
  await detectIPTypeForRefs(refs)
}

async function detectIPTypeForRefs(refs) {
  if (detectingIPType.value || !refs?.length) return
  const queue = refs.map(r => ({ type: r.type, index: r.index, sub: r.sub || 0 }))
  detectingIPType.value = true
  const states = {}
  for (const ref of queue) states[refKey(ref)] = { status: 'running' }
  ipTypeStates.value = states
  try {
    await api.detectServerIPType(queue)
    await store.fetchAll()
  } catch (e) {
    console.error('IP 类型检测失败', e)
  } finally {
    ipTypeStates.value = {}
    detectingIPType.value = false
  }
}

// 查询当前 tab 全部节点的 IP 评分
async function scoreIPForCurrentTab() {
  let refs = []
  if (activeTab.value.startsWith('sub-')) {
    const si = parseInt(activeTab.value.replace('sub-', ''))
    const sub = store.subscriptions[si]
    refs = (sub?.servers || []).map((_, i) => ({ type: 'sub_server', index: i, sub: si }))
  } else if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    refs = (group?.servers || []).map(r => ({ type: r.type, index: r.index, sub: r.sub || 0 }))
  }
  await scoreIPForRefs(refs)
}

async function scoreIPForRefs(refs) {
  if (ipScoreTask.value.running || !refs?.length) return
  const queue = refs.map(r => ({ type: r.type, index: r.index, sub: r.sub || 0 }))
  const marks = {}
  for (const ref of queue) marks[refKey(ref)] = true
  ipScoringRefs.value = marks
  try {
    await api.scoreServerIP(queue)
    showNotice(`已在后台开始查询 ${queue.length} 个节点的 IP 评分，按限速逐个进行，请稍后在「评分」列查看结果。`)
    startIPScorePoll()
  } catch (e) {
    ipScoringRefs.value = {}
    showNotice(e?.response?.data?.error || 'IP 评分启动失败', true)
  }
}

function startIPScorePoll() {
  stopIPScorePoll()
  ipScoreTask.value = { ...ipScoreTask.value, running: true }
  ipScorePollTimer = setInterval(pollIPScore, 1000)
  pollIPScore()
}

function stopIPScorePoll() {
  if (ipScorePollTimer) {
    clearInterval(ipScorePollTimer)
    ipScorePollTimer = null
  }
}

async function pollIPScore() {
  try {
    const { data } = await api.getIPScoreStatus()
    const wasRunning = ipScoreTask.value.running
    ipScoreTask.value = data
    if (!data.running) {
      stopIPScorePoll()
      ipScoringRefs.value = {}
      await store.fetchAll()
      if (wasRunning) {
        const failed = data.failed ? `，${data.failed} 个失败` : ''
        showNotice(`IP 评分完成：成功 ${Math.max(data.done - data.failed, 0)}/${data.total}${failed}。`)
      }
    }
  } catch (e) {
    stopIPScorePoll()
    ipScoringRefs.value = {}
  }
}

async function abortIPScore() {
  try {
    await api.cancelIPScore()
  } catch (e) {
    console.error('中止评分失败', e)
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
  editSubForm.value = {
    name: sub.name || '',
    url: sub.url || '',
    format: sub.format || 'auto',
    proxyOutbound: sub.proxyOutbound || '',
  }
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

async function openAddToGroup(gi) {
  showAddToGroup.value = gi
  selectedAddIdx.value = -1
  addSearch.value = ''
  const [sRes, subRes] = await Promise.all([api.getServers(), api.getSubscriptions()])
  const result = []
  ;(sRes.data.servers || []).forEach((s, i) => result.push({ ...s, _ref: { type: 'server', index: i, sub: 0 } }))
  ;(subRes.data.subscriptions || []).forEach((sub, si) => {
    ;(sub.servers || []).forEach((s, i) => result.push({ ...s, _ref: { type: 'sub_server', index: i, sub: si } }))
  })
  selectableServers.value = result
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
    await api.copyServerToGroup(ref, bulkMoveGroupId.value, false)
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
  await api.copyServerToGroup(singleMoveRef.value, singleTargetGroupId.value, false)
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
/* Tab 视口：承载左右渐变遮罩，指示还有未显示的 tab */
.tabs-viewport {
  position: relative;
  flex: 1 1 auto;
  min-width: 0;
  display: flex;
  overflow: hidden;
}
.tabs-viewport.has-left::before,
.tabs-viewport.has-right::after {
  content: '';
  position: absolute;
  top: 0;
  bottom: 0;
  width: 28px;
  pointer-events: none;
  z-index: 2;
}
.tabs-viewport.has-left::before {
  left: 0;
  background: linear-gradient(to right, #fff, rgba(255,255,255,0));
}
.tabs-viewport.has-right::after {
  right: 0;
  background: linear-gradient(to left, #fff, rgba(255,255,255,0));
}

.tabs-wrap {
  display: flex;
  align-items: stretch;
  min-width: 0;
  flex: 1 1 auto;
  overflow-x: auto;
  overflow-y: hidden;
  gap: 0;
  scrollbar-width: none;
}
.tabs-wrap::-webkit-scrollbar { display: none; }

/* 左右滚动按钮 */
.tab-scroll-btn {
  flex-shrink: 0;
  align-self: center;
  width: 24px;
  height: 30px;
  border: 1px solid #e8e8e8;
  border-radius: 4px;
  background: #fff;
  color: #595959;
  font-size: 16px;
  line-height: 1;
  cursor: pointer;
  display: flex;
  align-items: center;
  justify-content: center;
  transition: all .15s;
  user-select: none;
}
.tab-scroll-btn:hover:not(:disabled) {
  border-color: #1677ff;
  color: #1677ff;
  background: #f5f8ff;
}
.tab-scroll-btn:disabled {
  opacity: .35;
  cursor: default;
}

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
.ping-progress {
  font-size: 12px;
  color: #1677ff;
  max-width: 280px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
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

/* ===== 后台任务提示条 ===== */
.notice-bar {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 9px 24px;
  background: #f6ffed;
  border-bottom: 1px solid #b7eb8f;
  color: #237804;
  font-size: 13px;
  flex-shrink: 0;
}
.notice-bar-error {
  background: #fff2f0;
  border-bottom-color: #ffccc7;
  color: #a8071a;
}
.notice-close {
  margin-left: auto;
  border: none;
  background: transparent;
  color: inherit;
  opacity: .6;
  cursor: pointer;
  font-size: 12px;
  padding: 0 4px;
}
.notice-close:hover { opacity: 1; }

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
  overflow: hidden;
  padding: 20px 24px;
  max-width: 1200px;
  width: 100%;
  margin: 0 auto;
  box-sizing: border-box;
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.panel {
  background: #fff;
  border-radius: 8px;
  border: 1px solid #e8e8e8;
  overflow: hidden;
  box-shadow: 0 1px 4px rgba(0,0,0,.04);
}
.panel-fill {
  display: flex;
  flex-direction: column;
  flex: 1;
  min-height: 0;
}
/* 表头和行放在同一个滚动容器里，横向滚动时两者才不会错位。
   列宽固定后总宽可能超过面板，窄窗口下靠横向滚动而不是挤压列。 */
.table-scroll {
  flex: 1;
  min-height: 0;
  overflow: auto;
}
.table-scroll > :first-child {
  position: sticky;
  top: 0;
  z-index: 2;
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
.form-hint {
  font-size: 11px;
  color: #8c8c8c;
  margin-top: 4px;
  line-height: 1.5;
}
</style>
