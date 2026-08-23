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
              <span class="tab-badge">{{ groupServerCount(group) }}</span>
            </span>
            <span
              v-for="(sub, si) in store.subscriptions" :key="'sub-' + sub.id"
              :data-tab-key="'sub-' + si"
              :class="['tab', activeTab === 'sub-' + si ? 'tab-active' : '']"
              @click="activeTab = 'sub-' + si"
            >
              {{ sub.name || sub.host }}
              <span class="tab-badge">{{ sub.serverCount ?? sub.servers?.length ?? 0 }}</span>
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
        <div class="tab-actions tab-actions-desktop">
          <button class="action-btn action-btn-primary" @click="openImport">＋ 导入</button>
          <button class="action-btn" @click="showNewGroup = true">📁 新建分组</button>
        </div>
        <details class="mobile-tab-actions" ref="mobileTabActionsEl">
          <summary title="更多操作">＋</summary>
          <div class="mobile-tab-action-menu">
            <button @click="openMobileImport">＋ 导入</button>
            <button @click="openMobileNewGroup">📁 新建分组</button>
          </div>
        </details>
      </div>
    </div>

    <!-- 当前出站状态条已移至 App.vue 全局显示 -->
    <div class="content-area">

      <div v-if="notice" :class="['notice-bar', { 'notice-bar-error': notice.error }]">
        <span>{{ notice.text }}</span>
        <button class="notice-close" @click="notice = null" title="关闭">✕</button>
      </div>

      <!-- 多选工具栏（有选中时显示） -->
      <div class="bulk-bar" v-if="hasSelection">
        <span class="bulk-count">已选 {{ selectedRefs.length }} 个节点</span>
        <button class="action-btn" @click="selectAll" v-if="!allSelected">☑ 全选</button>
        <button class="action-btn" @click="clearSelection" v-if="allSelected">☐ 取消全选</button>
        <button v-if="singleSelectedRef" class="action-btn action-btn-primary mobile-only-action" @click="connectNode(singleSelectedRef)">
          🔗 连接 / 切换
        </button>
        <button v-if="singleSelectedRef && singleSelectedServer" class="action-btn mobile-only-action" @click="openNodeMenu('edit', singleSelectedRef, singleSelectedServer)">
          ✏ 编辑
        </button>
        <button
          :class="['action-btn', { 'action-btn-danger': pinging && activePingMode === 'fast' }]"
          :disabled="pinging && activePingMode !== 'fast'"
          @click="pinging && activePingMode === 'fast' ? abortPing() : pingSelected('fast')"
        >{{ pinging && activePingMode === 'fast' ? '⏹ 中止检测' : '⚡ 批量快速' }}</button>
        <button
          :class="['action-btn', { 'action-btn-danger': pinging && activePingMode === 'real' }]"
          :disabled="pinging && activePingMode !== 'real'"
          @click="pinging && activePingMode === 'real' ? abortPing() : pingSelected('real')"
        >{{ pinging && activePingMode === 'real' ? '⏹ 中止检测' : '🧪 批量真连' }}</button>
        <button class="action-btn" @click="detectIPTypeForRefs(selectedRefs)" :disabled="detectingIPType">
          🌐 {{ detectingIPType ? '检测中…' : '检测 IP 类型' }}
        </button>
        <button
          :class="['action-btn', { 'action-btn-danger': ipScoreTask.running }]"
          @click="ipScoreTask.running ? abortIPScore() : scoreIPForRefs(selectedRefs)"
          title="查询 IP 纯净度评分（后台串行执行）"
        >
          {{ ipScoreTask.running ? '⏹ 中止评分' : '🧮 查 IP 评分' }}
        </button>
        <button class="action-btn" @click="shareSelected">🔗 批量分享</button>
        <button class="action-btn" @click="showBulkCopyModal = true">📋 复制到分组</button>
        <button v-if="canBulkRemove" class="action-btn" @click="showBulkMoveModal = true">✂ 移动到分组</button>
        <button v-if="canBulkRemove" class="action-btn action-btn-danger" @click="removeSelected">✕ 批量删除</button>
        <button class="action-btn" @click="clearSelection">取消选择</button>
      </div>

      <!-- 订阅 tab -->
      <template v-for="(sub, si) in store.subscriptions" :key="'sub-' + sub.id">
        <div v-if="activeTab === 'sub-' + si" class="panel panel-fill">
          <div class="info-bar">
            <div class="info-bar-left">
              <span class="info-url" :title="sub.url">{{ sub.url }}</span>
              <span v-if="sub.updatedAt" class="info-time">{{ formatTime(sub.updatedAt) }}</span>
              <span v-if="isSearching" class="info-count">筛选结果：共 {{ searchTotal }} 个</span>
            </div>
            <div class="info-bar-right" v-if="!hasSelection">
              <button :class="['action-btn', { 'filter-btn-active': showSearchFilters || isSearching }]" @click="showSearchFilters = !showSearchFilters">
                {{ showSearchFilters ? '▴ 收起' : '⚙ 搜索 / 筛选' }}<span v-if="searchConditionCount">{{ searchConditionCount }}</span>
              </button>
              <button class="action-btn" @click="refreshPanel" :disabled="refreshing" title="重新加载节点数据">
                ↻ {{ refreshing ? '刷新中…' : '刷新' }}
              </button>
              <button
                :class="['action-btn', { 'action-btn-danger': pinging && activePingMode === 'fast' }]"
                :disabled="pinging && activePingMode !== 'fast'"
                @click="pinging && activePingMode === 'fast' ? abortPing() : pingCurrentTab('fast')"
              >{{ pinging && activePingMode === 'fast' ? '⏹ 中止检测' : '⚡ 快速检测' }}</button>
              <button
                :class="['action-btn', { 'action-btn-danger': pinging && activePingMode === 'real' }]"
                :disabled="pinging && activePingMode !== 'real'"
                @click="pinging && activePingMode === 'real' ? abortPing() : pingCurrentTab('real')"
              >{{ pinging && activePingMode === 'real' ? '⏹ 中止检测' : '🧪 真连接检测' }}</button>
              <button class="action-btn" @click="detectIPTypeForCurrentTab" :disabled="detectingIPType" title="查询节点地址是机房还是家宽">
                🌐 {{ detectingIPType ? '检测中…' : 'IP 类型' }}
              </button>
              <button
                :class="['action-btn', { 'action-btn-danger': ipScoreTask.running }]"
                @click="ipScoreTask.running ? abortIPScore() : scoreIPForCurrentTab()"
                title="查询 IP 纯净度评分（后台串行执行，避免触发第三方限流）"
              >
                {{ ipScoreTask.running ? '⏹ 中止评分' : '🧮 IP 评分' }}
              </button>
              <button class="action-btn" @click="refreshSub(si)" title="重新拉取订阅节点">↻ 更新节点</button>
              <button class="action-btn" @click="openEditSub(si)" title="编辑订阅名称和链接">✏ 编辑</button>
              <button class="action-btn action-btn-danger" @click="deleteSub(si)">删除</button>
            </div>
          </div>
          <NodeFilterPanel
            v-if="showSearchFilters && !hasSelection"
            :query="searchQuery"
            :filters="searchFilters"
            :searching="isSearching"
            @update:query="searchQuery = $event"
            @update:filters="searchFilters = $event"
            @search="submitSearch"
            @reset="resetSearchFilters"
            @clear="closeSearch"
            @close="showSearchFilters = false"
          />
          <div v-if="isSearching && !sortedSearchResults.length" class="empty-hint">
            <p class="empty-sub">无匹配节点</p>
          </div>
          <div v-else-if="!isSearching && !sub.serversLoaded" class="empty-hint">
            <p class="empty-sub">正在加载订阅节点…</p>
          </div>
          <div v-else-if="!isSearching && !sub.servers.length" class="empty-hint">
            <p class="empty-sub">订阅为空，点击「更新节点」拉取</p>
          </div>
          <div class="table-scroll" v-if="getDisplayedSubEntries(sub, si).length">
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
              v-for="(item, i) in getDisplayedSubEntries(sub, si)" :key="refKey(item.ref) + ':' + i"
              :server="item.server" :ref-obj="item.ref"
              :outbounds="store.outbounds" :removable="false"
              :group-names="item.groupNames || []"
              :current-outbound="currentOutbound"
              :ping-state="pingStates[refKey(item.ref)] || null"
              :ip-type-state="ipTypeStates[refKey(item.ref)] || null"
              :ip-score-running="!!ipScoringRefs[refKey(item.ref)]"
              :selected="isSelected(item.ref)"
              @connect="connectNode"
              @select="toggleSelect"
              @select-start="startDragSelect"
              @select-enter="continueDragSelect"
              @edit="(r, sv) => openNodeMenu('edit', r, sv)"
              @share="(r, sv) => openNodeMenu('share', r, sv)"
              @copy-to-group="ref => openSingleCopy(ref)"
            />
          </div>
          <div class="node-pagination" v-if="currentNodeTotal > 0">
            <button class="action-btn" :disabled="nodePage <= 1" @click="changeNodePage(nodePage - 1)">‹ 上一页</button>
            <span>第 {{ nodePage }} / {{ nodeTotalPages }} 页，共 {{ currentNodeTotal }} 个</span>
            <button class="action-btn" :disabled="nodePage >= nodeTotalPages" @click="changeNodePage(nodePage + 1)">下一页 ›</button>
            <label class="node-page-size">
              每页
              <select v-model.number="nodePageSize" @change="changeNodePageSize">
                <option v-for="option in NODE_PAGE_SIZE_OPTIONS" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
            </label>
          </div>
        </div>
      </template>

      <!-- 手动分组 tab -->
      <template v-for="(group, gi) in manualGroups" :key="'group-' + group.id">
        <div v-if="activeTab === 'group-' + gi" class="panel panel-fill">
          <div class="info-bar">
            <div class="info-bar-left">
              <span class="group-name-display">{{ group.name }}</span>
              <span class="info-count">{{ isSearching ? `筛选结果：共 ${searchTotal} 个` : `${groupServerCount(group)} 个节点` }}</span>
            </div>
            <div class="info-bar-right" v-if="!hasSelection">
              <button :class="['action-btn', { 'filter-btn-active': showSearchFilters || isSearching }]" @click="showSearchFilters = !showSearchFilters">
                {{ showSearchFilters ? '▴ 收起' : '⚙ 搜索 / 筛选' }}<span v-if="searchConditionCount">{{ searchConditionCount }}</span>
              </button>
              <button class="action-btn" @click="refreshPanel" :disabled="refreshing" title="重新加载节点数据">
                ↻ {{ refreshing ? '刷新中…' : '刷新' }}
              </button>
              <button
                :class="['action-btn', { 'action-btn-danger': pinging && activePingMode === 'fast' }]"
                :disabled="pinging && activePingMode !== 'fast'"
                @click="pinging && activePingMode === 'fast' ? abortPing() : pingCurrentTab('fast')"
              >{{ pinging && activePingMode === 'fast' ? '⏹ 中止检测' : '⚡ 快速检测' }}</button>
              <button
                :class="['action-btn', { 'action-btn-danger': pinging && activePingMode === 'real' }]"
                :disabled="pinging && activePingMode !== 'real'"
                @click="pinging && activePingMode === 'real' ? abortPing() : pingCurrentTab('real')"
              >{{ pinging && activePingMode === 'real' ? '⏹ 中止检测' : '🧪 真连接检测' }}</button>
              <button class="action-btn" @click="detectIPTypeForCurrentTab" :disabled="detectingIPType" title="查询节点地址是机房还是家宽">
                🌐 {{ detectingIPType ? '检测中…' : 'IP 类型' }}
              </button>
              <button
                :class="['action-btn', { 'action-btn-danger': ipScoreTask.running }]"
                @click="ipScoreTask.running ? abortIPScore() : scoreIPForCurrentTab()"
                title="查询 IP 纯净度评分（后台串行执行，避免触发第三方限流）"
              >
                {{ ipScoreTask.running ? '⏹ 中止评分' : '🧮 IP 评分' }}
              </button>
              <button v-if="group.name !== 'SERVER'" class="action-btn" @click="openEditGroup(gi)">✏ 编辑</button>
              <button class="action-btn" @click="openAddToGroup(gi)">＋ 添加节点</button>
              <button v-if="group.name !== 'SERVER'" class="action-btn action-btn-danger" @click="deleteGroup(gi)">删除分组</button>
            </div>
          </div>
          <NodeFilterPanel
            v-if="showSearchFilters && !hasSelection"
            :query="searchQuery"
            :filters="searchFilters"
            :searching="isSearching"
            @update:query="searchQuery = $event"
            @update:filters="searchFilters = $event"
            @search="submitSearch"
            @reset="resetSearchFilters"
            @clear="closeSearch"
            @close="showSearchFilters = false"
          />
          <div v-if="isSearching && !sortedSearchResults.length" class="empty-hint">
            <p class="empty-sub">无匹配节点</p>
          </div>
          <div v-else-if="!isSearching && groupServerCount(group) === 0" class="empty-hint">
            <p class="empty-sub">分组为空，点击「添加节点」</p>
          </div>
          <div v-else-if="!isSearching && !getSortedGroupEntries(group).length" class="empty-hint">
            <p class="empty-sub">正在加载分组节点…</p>
          </div>
          <div class="table-scroll" v-if="getDisplayedGroupEntries(group).length">
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
              v-for="(item, i) in getDisplayedGroupEntries(group)" :key="refKey(item.ref) + ':' + i"
              :server="item.server" :ref-obj="item.ref" :outbounds="store.outbounds" :removable="!isSearching"
              :group-names="item.groupNames || []"
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
          <div class="node-pagination" v-if="currentNodeTotal > 0">
            <button class="action-btn" :disabled="nodePage <= 1" @click="changeNodePage(nodePage - 1)">‹ 上一页</button>
            <span>第 {{ nodePage }} / {{ nodeTotalPages }} 页，共 {{ currentNodeTotal }} 个</span>
            <button class="action-btn" :disabled="nodePage >= nodeTotalPages" @click="changeNodePage(nodePage + 1)">下一页 ›</button>
            <label class="node-page-size">
              每页
              <select v-model.number="nodePageSize" @change="changeNodePageSize">
                <option v-for="option in NODE_PAGE_SIZE_OPTIONS" :key="option.value" :value="option.value">
                  {{ option.label }}
                </option>
              </select>
            </label>
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
      @updated="handleNodeUpdated"
      @close="nodeMenuMode = ''"
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
          <input class="input" v-model="addSearch" placeholder="输入名称、地址或协议搜索…" style="font-size:12px" />
        </div>
        <div class="modal-body" style="padding:0; max-height:400px; overflow-y:auto">
          <div
            v-for="(s, i) in filteredSelectableServers" :key="refKey(s._ref)"
            :class="['sel-row', selectedAddIdx === i ? 'sel-row-active' : '']"
            @click="selectedAddIdx = i"
          >
            <span :class="['tag', `tag-${s.type || 'vmess'}`]">{{ s.type || '?' }}</span>
            <span class="sel-name">{{ s.name || '未命名' }}</span>
            <span class="sel-addr">{{ s.host }}:{{ s.port }}</span>
          </div>
          <div v-if="!filteredSelectableServers.length" style="padding:20px; text-align:center; color:#b5b5b5; font-size:13px">
            {{ addSearch.trim() ? '无匹配节点' : '输入关键字后从服务端查询节点' }}
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
    <div class="modal-overlay" v-if="showBulkCopyModal" @mousedown.self="!bulkCopying && (showBulkCopyModal = false)">
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
                {{ g.name }}（{{ groupServerCount(g) }} 个节点）
              </option>
            </select>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" :disabled="bulkCopying" @click="showBulkCopyModal = false">取消</button>
          <button class="btn btn-primary" :disabled="!bulkCopyGroupId || bulkCopying" @click="confirmBulkCopy">
            {{ bulkCopying ? '复制中…' : '复制' }}
          </button>
        </div>
      </div>
    </div>

    <!-- 批量移动到分组弹窗 -->
    <div class="modal-overlay" v-if="showBulkMoveModal" @mousedown.self="!bulkMoving && (showBulkMoveModal = false)">
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
                {{ g.name }}（{{ groupServerCount(g) }} 个节点）
              </option>
            </select>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn btn-light" :disabled="bulkMoving" @click="showBulkMoveModal = false">取消</button>
          <button class="btn btn-primary" :disabled="!bulkMoveGroupId || bulkMoving" @click="confirmBulkMove">
            {{ bulkMoving ? '移动中…' : '移动' }}
          </button>
        </div>
      </div>
    </div>

    <!-- 单节点复制到分组弹窗 -->
    <div class="modal-overlay" v-if="showSingleCopyModal" @mousedown.self="!singleCopying && (showSingleCopyModal = false)">
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
          <button class="btn btn-light" :disabled="singleCopying" @click="showSingleCopyModal = false">取消</button>
          <button class="btn btn-primary" :disabled="!singleTargetGroupId || singleCopying" @click="confirmSingleCopy">
            {{ singleCopying ? '复制中…' : '复制' }}
          </button>
        </div>
      </div>
    </div>

    <!-- 单节点移动到分组弹窗 -->
    <div class="modal-overlay" v-if="showSingleMoveModal" @mousedown.self="!singleMoving && (showSingleMoveModal = false)">
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
          <button class="btn btn-light" :disabled="singleMoving" @click="showSingleMoveModal = false">取消</button>
          <button class="btn btn-primary" :disabled="!singleTargetGroupId || singleMoving" @click="confirmSingleMove">
            {{ singleMoving ? '移动中…' : '移动' }}
          </button>
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
import NodeFilterPanel from '../components/NodeFilterPanel.vue'
import NodeMenu from '../components/NodeMenu.vue'
import axios from 'axios'

const store = useProxyStore()
const openImport = inject('openImport')
const currentOutbound = inject('currentOutbound')
const outboundSelectionVersion = inject('outboundSelectionVersion', ref(0))

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
const appliedSearchQuery = ref('')
const searchResults = ref([])
const searchTotal = ref(0)
const isSearching = ref(false)
const showSearchFilters = ref(false)
function emptySearchFilters() {
  return {
  type: '',
  geo: '',
  source: '',
  ipType: '',
  fastProbe: '',
  probe: '',
  maxLatency: '',
  minScore: '',
  }
}
const searchFilters = ref(emptySearchFilters())
const appliedSearchFilters = ref(emptySearchFilters())
const pingProgress = ref({ total: 0, done: 0, current: '' })
const pingStates = ref({})
const activePingMode = ref('')
const transProxyFallbackEnabled = ref(false)
let previousTransProxyFallbackCommand
let installedTransProxyFallbackCommand
const FAST_PING_CONCURRENCY = 6
const NODE_PAGE_SIZE_OPTIONS = [
  { value: 100, label: '100 条' },
  { value: 500, label: '500 条' },
  { value: 1000, label: '1000 条' },
  { value: 0, label: '全部' },
]
const nodePageSize = ref(100)
const nodePage = ref(1)

// ---- 列宽 ----
// 操作列也是一列：6 个按钮实际要 220px 左右，之前固定 180px 且右对齐 + 裁剪，
// 导致最左侧的按钮被切掉。放进来后既有足够默认宽度，也能像其他列一样拖动调整。
const COLUMN_DEFS = [
  { key: 'name', label: '节点', min: 80, def: 170, sortable: true },
  { key: 'addr', label: '地址', min: 90, def: 190, sortable: true },
  { key: 'geo', label: '地区', min: 80, def: 120, sortable: true },
  { key: 'type', label: '协议', min: 50, def: 68, sortable: true },
  { key: 'frontProxy', label: '前置代理', min: 64, def: 88, sortable: true },
  { key: 'iptype', label: 'IP 类型', min: 46, def: 56, sortable: true },
  { key: 'ipscore', label: '评分', min: 46, def: 56, sortable: true },
  { key: 'fastLatency', label: '快速延时', min: 54, def: 66, sortable: true },
  { key: 'latency', label: '真连延时', min: 54, def: 70, sortable: true },
  { key: 'ops', label: '操作', min: 120, def: 220, noResizer: true },
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
  if (key === 'fastLatency') return latencyValue(server?.fastLatency, server?.lastFastProbeTime)
  if (key === 'latency') return latencyValue(server?.latency, server?.lastProbeTime)
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
  if (key === 'frontProxy') return compareText(a?.frontProxy || '直连', b?.frontProxy || '直连')
  if (key === 'geo') {
    return compareText(a?.ipGeoCountry, b?.ipGeoCountry) ||
      compareText(a?.ipGeoRegion, b?.ipGeoRegion) ||
      compareText(a?.ipGeoCity, b?.ipGeoCity)
  }
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

function latencyValue(rawLatency, rawProbeTime) {
  const latency = Number(rawLatency)
  const probeTime = Number(rawProbeTime)
  if (!Number.isFinite(probeTime) || probeTime <= 0) return Number.MAX_SAFE_INTEGER
  if (!Number.isFinite(latency) || latency <= 0) return Number.MAX_SAFE_INTEGER - 1
  return latency
}

// 只在点击表头时，对当前可见行生成一次 ref -> 名次快照。
// 后续延迟、评分、地区等字段变化只更新单元格，不重新计算名次。
function captureSortOrder() {
  if (!sortKey.value) {
    sortRanks.value = null
    return
  }
  const entries = getCurrentEntriesForSort().map((entry, index) => ({ entry, index }))
  const dir = sortDir.value === 'desc' ? -1 : 1
  entries.sort((a, b) => {
    const result = compareServers(sortKey.value, a.entry.server, b.entry.server) * dir
    return result || a.index - b.index
  })

  const ranks = new Map()
  entries.forEach(({ entry }, index) => {
    ranks.set(refKey(entry.ref), index)
  })
  sortRanks.value = ranks
}

function getCurrentEntriesForSort() {
  if (isSearching.value) return searchResults.value
  if (activeTab.value.startsWith('sub-')) {
    const si = parseInt(activeTab.value.replace('sub-', ''))
    return getSubServerEntries(store.subscriptions[si], si)
  }
  if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    return getGroupServerEntries(manualGroups.value[gi])
  }
  return []
}

// 按点击表头时固定下来的名次排列。之后出现的新行没有名次，保持在末尾。
function applySortOrder(entries) {
  const ranks = sortRanks.value
  if (!ranks) return entries
  const decorated = entries.map(entry => ({
    entry,
    rank: ranks.get(refKey(entry.ref)) ?? Number.POSITIVE_INFINITY,
  }))
  decorated.sort((a, b) => a.rank - b.rank)
  return decorated.map(item => item.entry)
}
const pingAbortController = ref(null)
const pingAborted = ref(false)
let pendingPingStates = {}
let pendingPingPatches = []
let pingFlushFrame = 0

function schedulePingFlush(key, state, refObj, patch) {
  pendingPingStates[key] = state
  if (refObj && patch) pendingPingPatches.push({ ref: refObj, patch })
  if (pingFlushFrame) return
  pingFlushFrame = window.requestAnimationFrame(flushPendingPingUpdates)
}

function flushPendingPingUpdates() {
  if (pingFlushFrame) window.cancelAnimationFrame(pingFlushFrame)
  if (Object.keys(pendingPingStates).length) {
    pingStates.value = { ...pingStates.value, ...pendingPingStates }
  }
  if (pendingPingPatches.length) store.patchNodes(pendingPingPatches)
  pendingPingStates = {}
  pendingPingPatches = []
  pingFlushFrame = 0
}

function cancelPingFlush() {
  if (pingFlushFrame) window.cancelAnimationFrame(pingFlushFrame)
  pingFlushFrame = 0
  pendingPingStates = {}
  pendingPingPatches = []
}

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
    if (isSearching.value) {
      await onSearch(false)
      return
    }
    // 根据当前 tab 只刷新表格数据
    if (activeTab.value.startsWith('sub-')) {
      const si = parseInt(activeTab.value.replace('sub-', ''))
      await store.fetchSubscriptionServers(si, true, nodePage.value, nodePageSize.value)
    } else if (activeTab.value.startsWith('group-')) {
      const gi = parseInt(activeTab.value.replace('group-', ''))
      const group = manualGroups.value[gi]
      if (group) await store.fetchGroupServers(realGroupIndex(group), true, nodePage.value, nodePageSize.value)
    }
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
const bulkCopying = ref(false)

// 单节点复制/移动到分组
const showSingleCopyModal = ref(false)
const showSingleMoveModal = ref(false)
const singleCopyRef = ref(null)
const singleMoveRef = ref(null)
const singleTargetGroupId = ref('')
const singleCopying = ref(false)
const singleMoving = ref(false)

// NodeMenu 状态
const nodeMenuMode = ref('')
const nodeMenuRef = ref(null)
const nodeMenuServer = ref(null)

// 多选状态
const selectedRefs = ref([])
const hasSelection = computed(() => selectedRefs.value.length > 0)
const singleSelectedRef = computed(() => selectedRefs.value.length === 1 ? selectedRefs.value[0] : null)
const singleSelectedServer = computed(() => {
  const refObj = singleSelectedRef.value
  if (!refObj) return null
  const searchItem = searchResults.value.find(item => refKey(item.ref) === refKey(refObj))
  if (searchItem?.server) return searchItem.server
  if (refObj.type === 'server') return store.servers[refObj.index] || null
  return store.subscriptions[refObj.sub || 0]?.servers?.[refObj.index] || null
})
const selectedKeys = computed(() => new Set(selectedRefs.value.map(refKey)))
const selectionAnchorKey = ref('')
const dragSelecting = ref(false)
const dragSelectChecked = ref(true)
const dragLastKey = ref('')
const dragStartPos = ref({ x: 0, y: 0 }) // 记录拖拽开始位置
const dragMoved = ref(false) // 标记是否真正发生了拖拽移动

// ===== Tab 栏横向滚动 =====
const tabsWrapEl = ref(null)
const mobileTabActionsEl = ref(null)
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

function closeMobileTabActions() {
  mobileTabActionsEl.value?.removeAttribute('open')
}

function openMobileImport() {
  closeMobileTabActions()
  openImport()
}

function openMobileNewGroup() {
  closeMobileTabActions()
  showNewGroup.value = true
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

onMounted(async () => {
  const proxyStation = window.ProxyStation || (window.ProxyStation = {})
  previousTransProxyFallbackCommand = proxyStation.transProxyFallback
  installedTransProxyFallbackCommand = (enabled) => {
    if (typeof enabled === 'boolean') {
      transProxyFallbackEnabled.value = enabled
      console.info(`[ProxyStation] 真连接 trans-proxy 回退已${enabled ? '开启' : '关闭'}`)
    }
    return transProxyFallbackEnabled.value
  }
  proxyStation.transProxyFallback = installedTransProxyFallbackCommand
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
  await store.fetchSubscriptions(false)
  ensureActiveTabNodes()
})

onBeforeUnmount(() => {
  const proxyStation = window.ProxyStation
  if (proxyStation && proxyStation.transProxyFallback === installedTransProxyFallbackCommand) {
    if (previousTransProxyFallbackCommand) proxyStation.transProxyFallback = previousTransProxyFallbackCommand
    else delete proxyStation.transProxyFallback
  }
  tabsResizeObserver?.disconnect()
  window.removeEventListener('resize', updateTabScrollState)
  window.removeEventListener('pointerup', endDragSelect)
  window.removeEventListener('pointercancel', endDragSelect)
  window.removeEventListener('blur', endDragSelect)
  stopIPScorePoll()
  clearTimeout(addSearchTimer)
  searchAbortController?.abort()
  addSearchAbortController?.abort()
  cancelPingFlush()
})

async function resumeIPScoreIfRunning() {
  try {
    const { data } = await api.getIPScoreStatus()
    const state = data.state || data
    ipScoreTask.value = state
    if (state?.running) startIPScorePoll()
  } catch {}
}

function refKey(ref) {
  return `${ref.type}:${ref.index}:${ref.sub || 0}`
}
function isSelected(ref) {
  return selectedKeys.value.has(refKey(ref))
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
  const currentTabServers = getCurrentTabServers()
  return currentTabServers.length > 0 && currentTabServers.every(ref => selectedKeys.value.has(refKey(ref)))
})

function getCurrentTabServers() {
  if (isSearching.value) return sortedSearchResults.value.map(item => item.ref)
  if (activeTab.value.startsWith('sub-')) {
    const si = parseInt(activeTab.value.replace('sub-', ''))
    const sub = store.subscriptions[si]
    const start = nodePageSize.value > 0 ? (nodePage.value - 1) * nodePageSize.value : 0
    const end = nodePageSize.value > 0
      ? Math.min(start + nodePageSize.value, Number(sub?.serverCount || 0))
      : Number(sub?.serverCount || 0)
    return Array.from({ length: Math.max(0, end - start) }, (_, offset) => ({
      type: 'sub_server', index: start + offset, sub: si,
    }))
  }
  if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    return getGroupPageRefs(group)
  }
  return []
}

function getSortedCurrentTabRefs() {
  if (isSearching.value) return sortedSearchResults.value.map(e => e.ref)
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

function clearSelection() {
  selectedRefs.value = []
  showSelectAll.value = false
  selectionAnchorKey.value = ''
  endDragSelect()
}

// 切换 tab 时清空选择
watch(activeTab, async () => {
  nodePage.value = 1
  clearSortOrder()
  selectedRefs.value = []
  showSelectAll.value = false
  selectionAnchorKey.value = ''
  endDragSelect()
  await ensureActiveTabNodes()
  await nextTick()
  scrollActiveTabIntoView()
})

async function ensureActiveTabNodes() {
  if (isSearching.value) return
  if (activeTab.value.startsWith('sub-')) {
    const si = parseInt(activeTab.value.replace('sub-', ''))
    await store.fetchSubscriptionServers(si, false, nodePage.value, nodePageSize.value)
    return
  }
  if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    if (group) await store.fetchGroupServers(realGroupIndex(group), false, nodePage.value, nodePageSize.value)
  }
}

watch(
  () => [
    store.groups.map(group => `${group.id}:${group.serverCount || 0}`).join('|'),
    store.subscriptions.map(sub => `${sub.id}:${sub.serverCount || 0}`).join('|'),
  ],
  async () => {
    if (activeTab.value.startsWith('group-')) {
      const gi = parseInt(activeTab.value.replace('group-', ''))
      if (!manualGroups.value[gi] && manualGroups.value.length) activeTab.value = 'group-0'
    }
    await ensureActiveTabNodes()
  },
)

const manualGroups = computed(() => {
  const groups = store.groups.filter(group => !group.fromSub)
  const serverGroup = groups.find(group => group.name === 'SERVER')
  const otherGroups = groups.filter(group => group.name !== 'SERVER')
  otherGroups.sort((a, b) => {
    const timeA = new Date(a.createdAt || 0).getTime()
    const timeB = new Date(b.createdAt || 0).getTime()
    return timeA - timeB
  })
  return serverGroup ? [serverGroup, ...otherGroups] : otherGroups
})

function groupServerCount(group) {
  return Number(group?.serverCount ?? group?.servers?.length ?? 0)
}

const currentNodeTotal = computed(() => {
  if (isSearching.value) return searchTotal.value
  if (activeTab.value.startsWith('sub-')) {
    const si = parseInt(activeTab.value.replace('sub-', ''))
    return Number(store.subscriptions[si]?.serverCount || 0)
  }
  if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    return groupServerCount(manualGroups.value[gi])
  }
  return 0
})

const nodeTotalPages = computed(() => {
  if (nodePageSize.value <= 0) return 1
  return Math.max(1, Math.ceil(currentNodeTotal.value / nodePageSize.value))
})

watch(currentNodeTotal, () => {
  if (nodePage.value > nodeTotalPages.value) {
    nodePage.value = nodeTotalPages.value
    if (isSearching.value) onSearch(false)
    else ensureActiveTabNodes()
  }
})

async function changeNodePage(page) {
  nodePage.value = Math.min(Math.max(1, page), nodeTotalPages.value)
  clearSortOrder()
  selectedRefs.value = []
  if (isSearching.value) {
    await onSearch(false)
    return
  }
  await ensureActiveTabNodes()
}

async function changeNodePageSize() {
  nodePage.value = 1
  clearSortOrder()
  selectedRefs.value = []
  if (isSearching.value) {
    await onSearch(false)
    return
  }
  await ensureActiveTabNodes()
}

function clearSortOrder() {
  sortKey.value = ''
  sortDir.value = ''
  sortRanks.value = null
}

watch(isSearching, clearSortOrder)

// 当前 tab 是否可批量删除（只有手动分组可删）
const canBulkRemove = computed(() => {
  return activeTab.value.startsWith('group-')
})

// 一次性把所有 refs 提交给后端，由后端 worker pool 按配置的并发数调度
// （real 默认 4、fast 16），前端只消费 SSE 进度事件更新状态。
async function pingRefsWithProgress(refs, mode = 'fast') {
  const validRefs = (refs || []).filter(ref => ref && typeof ref.type === 'string')
  if (!validRefs.length) return

  pingProgress.value = { total: validRefs.length, done: 0, current: '' }
  pingAborted.value = false
  pingAbortController.value = new AbortController()

  const queue = validRefs.map(ref => ({ type: ref.type, index: ref.index, sub: ref.sub || 0 }))
  const enableTransProxyFallback = mode === 'real' && transProxyFallbackEnabled.value
  const stateMap = {}
  for (const ref of queue) {
    stateMap[refKey(ref)] = { status: 'pending', mode }
  }
  pingStates.value = stateMap

  try {
    await api.pingNodes(queue, mode, {
      signal: pingAbortController.value.signal,
      enableTransProxyFallback,
      onEvent: (evt, payload) => {
        const ref = payload?.ref
        if (!ref) return
        const key = refKey({ type: ref.type, index: ref.index, sub: ref.sub || 0 })
        if (evt === 'running') {
          schedulePingFlush(key, { status: 'running', mode })
        } else if (evt === 'fast') {
          const latency = typeof payload.latency === 'number' ? payload.latency : -1
          const probeTime = Math.floor(Date.now() / 1000)
          schedulePingFlush(key, null, ref, {
            fastLatency: latency,
            lastFastProbeTime: probeTime,
          })
        } else if (evt === 'done') {
          const lat = payload.latency
          const latency = typeof lat === 'number' ? lat : -1
          const probeTime = Math.floor(Date.now() / 1000)
          const patch = mode === 'fast'
            ? { fastLatency: latency, lastFastProbeTime: probeTime }
            : { latency, lastProbeTime: probeTime }
          schedulePingFlush(key, { status: 'done', mode, latency }, ref, patch)
          pingProgress.value.done += 1
        }
      },
    })
    flushPendingPingUpdates()
  } catch (e) {
    flushPendingPingUpdates()
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
  activePingMode.value = mode
  pinging.value = true
  try {
    await pingRefsWithProgress(selectedRefs.value, mode)
  } finally {
    pingAbortController.value = null
    pingProgress.value = { total: 0, done: 0, current: '' }
    pingStates.value = {}
    pinging.value = false
    activePingMode.value = ''
  }
}

async function shareSelected() {
  const links = await mapWithConcurrency(selectedRefs.value, async (ref) => {
    try {
      const { data } = await api.getServerLink(ref.type, ref.index, ref.sub || 0)
      return data.link || ''
    } catch {
      return ''
    }
  }, 8)
  const available = links.filter(Boolean)
  if (available.length) {
    await navigator.clipboard.writeText(available.join('\n'))
    alert(`已复制 ${available.length} 个节点链接到剪贴板`)
  }
}

async function mapWithConcurrency(items, worker, concurrency) {
  const results = new Array(items.length)
  let cursor = 0
  const runners = Array.from({ length: Math.min(concurrency, items.length) }, async () => {
    while (cursor < items.length) {
      const index = cursor++
      results[index] = await worker(items[index])
    }
  })
  await Promise.all(runners)
  return results
}

async function removeSelected() {
  if (!selectedRefs.value.length) return
  if (!confirm(`确认删除选中的 ${selectedRefs.value.length} 个节点？`)) return
  if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    const groupIndex = realGroupIndex(group)
    const refs = [...selectedRefs.value]
    const removeKeys = new Set(refs.map(refKey))
    store.patchGroup({ ...group, servers: (group.servers || []).filter(ref => !removeKeys.has(refKey(ref))) })
    try {
      const { data } = await api.removeServersFromGroup(groupIndex, refs)
      store.patchGroup(data.group)
    } catch (error) {
      store.patchGroup(group)
      throw error
    }
  }
  selectedRefs.value = []
}

function openNodeMenu(mode, refObj, server) {
  nodeMenuRef.value = refObj
  nodeMenuServer.value = server
  nodeMenuMode.value = mode
}

function handleNodeUpdated(refObj, server) {
  if (!refObj || !server) return
  const key = refKey(refObj)
  searchResults.value = searchResults.value.map(item =>
    refKey(item.ref) === key ? { ...item, server: { ...item.server, ...server } } : item
  )
}

let searchAbortController = null
const activeSearchFilterCount = computed(() => Object.values(searchFilters.value).filter(value => String(value).trim()).length)
const searchConditionCount = computed(() => activeSearchFilterCount.value + (searchQuery.value.trim() ? 1 : 0))

function searchRequestParams() {
  const pageSize = nodePageSize.value > 0 ? nodePageSize.value : 500
  const params = {
    limit: pageSize,
    offset: (nodePage.value - 1) * pageSize,
  }
  const query = appliedSearchQuery.value.trim()
  if (query) params.q = query
  for (const [key, value] of Object.entries(appliedSearchFilters.value)) {
    const normalized = String(value).trim()
    if (normalized) params[key] = normalized
  }
  return params
}

function closeSearch() {
  searchAbortController?.abort()
  searchQuery.value = ''
  appliedSearchQuery.value = ''
  searchFilters.value = emptySearchFilters()
  appliedSearchFilters.value = emptySearchFilters()
  showSearchFilters.value = false
  searchResults.value = []
  searchTotal.value = 0
  nodePage.value = 1
  isSearching.value = false
  ensureActiveTabNodes()
}

function resetSearchFilters() {
  searchFilters.value = emptySearchFilters()
  searchQuery.value = ''
}

function submitSearch() {
  appliedSearchQuery.value = searchQuery.value
  appliedSearchFilters.value = { ...searchFilters.value }
  onSearch()
}

async function onSearch(resetPage = true) {
  if (resetPage) nodePage.value = 1
  const hasCriteria = Boolean(appliedSearchQuery.value.trim()) || Object.values(appliedSearchFilters.value).some(value => String(value).trim())
  if (!hasCriteria) {
    searchAbortController?.abort()
    isSearching.value = false
    searchResults.value = []
    searchTotal.value = 0
    return
  }
  if (nodePageSize.value <= 0) nodePageSize.value = 500
  const params = searchRequestParams()
  searchAbortController?.abort()
  searchAbortController = new AbortController()
  const requestKey = JSON.stringify(params)
  isSearching.value = true
  try {
    const { data } = await api.searchServers(params, searchAbortController.signal)
    if (JSON.stringify(searchRequestParams()) !== requestKey) return
    searchResults.value = data.results || []
    searchTotal.value = Number(data.total || 0)
  } catch (error) {
    if (error?.code === 'ERR_CANCELED') return
    searchResults.value = []
    searchTotal.value = 0
  }
}

// tab 数量变化后要重算溢出状态（须在 manualGroups 定义之后注册，watch 的 getter 会立即求值）
watch(
  () => [manualGroups.value.length, store.subscriptions.length],
  async () => {
    await ensureActiveTabNodes()
    await nextTick()
    updateTabScrollState()
  }
)

watch(
  () => outboundSelectionVersion.value,
  (version) => {
    if (!version) return
    const outboundName = currentOutbound?.value || 'proxy'
    const outbound = store.outbounds.find(item => item.name === outboundName)
    const target = outbound?.target
    if (!target?.targetType) return

    if (target.targetType === 'group' && target.groupId) {
      const groupIndex = manualGroups.value.findIndex(group => group.id === target.groupId)
      if (groupIndex >= 0) activeTab.value = 'group-' + groupIndex
      return
    }

    if (target.targetType === 'node' && target.nodeRef) {
      const ref = target.nodeRef
      if (ref.type === 'sub_server') {
        const subscriptionIndex = Number(ref.sub || 0)
        if (subscriptionIndex >= 0 && subscriptionIndex < store.subscriptions.length) {
          activeTab.value = 'sub-' + subscriptionIndex
        }
        return
      }
      if (ref.type === 'server') {
        const groupIndex = manualGroups.value.findIndex(group =>
          (group.servers || []).some(item => item.type === 'server' && item.index === ref.index)
        )
        if (groupIndex >= 0) activeTab.value = 'group-' + groupIndex
      }
    }
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
  await Promise.all([store.fetchOutbounds(), store.fetchStatus()])
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
  return getGroupPageRefs(group).map(ref => {
    if (ref.type === 'server') return { ref, server: store.servers[ref.index] }
    const sub = store.subscriptions[ref.sub]
    return { ref, server: sub?.servers?.[ref.index] }
  }).filter(item => item.server)
}

function getSortedGroupEntries(group) {
  return applySortOrder(getGroupServerEntries(group))
}

function getSubServerEntries(sub, si) {
  const start = nodePageSize.value > 0 ? (nodePage.value - 1) * nodePageSize.value : 0
  const end = nodePageSize.value > 0
    ? Math.min(start + nodePageSize.value, Number(sub?.serverCount || 0))
    : Number(sub?.serverCount || 0)
  const entries = []
  for (let index = start; index < end; index += 1) {
    const server = sub?.servers?.[index]
    if (server) entries.push({ ref: { type: 'sub_server', index, sub: si }, server })
  }
  return entries
}

function getGroupPageRefs(group) {
  if (nodePageSize.value <= 0) return (group?.servers || []).filter(Boolean)
  const start = (nodePage.value - 1) * nodePageSize.value
  return (group?.servers || []).slice(start, start + nodePageSize.value).filter(Boolean)
}

function getSubscriptionRefs(si) {
  const total = Number(store.subscriptions[si]?.serverCount || 0)
  return Array.from({ length: total }, (_, index) => ({ type: 'sub_server', index, sub: si }))
}

function getSortedSubEntries(sub, si) {
  return applySortOrder(getSubServerEntries(sub, si))
}

const sortedSearchResults = computed(() => applySortOrder(searchResults.value))

function getDisplayedSubEntries(sub, si) {
  return isSearching.value ? sortedSearchResults.value : getSortedSubEntries(sub, si)
}

function getDisplayedGroupEntries(group) {
  return isSearching.value ? sortedSearchResults.value : getSortedGroupEntries(group)
}

const filteredSelectableServers = computed(() => {
  if (!addSearch.value.trim()) return selectableServers.value
  const q = addSearch.value.toLowerCase()
  return selectableServers.value.filter(s =>
    (s.name || '').toLowerCase().includes(q) ||
    (s.host || '').toLowerCase().includes(q)
  )
})

let addSearchTimer = null
let addSearchAbortController = null
watch(addSearch, (raw) => {
  clearTimeout(addSearchTimer)
  addSearchAbortController?.abort()
  selectedAddIdx.value = -1
  const query = raw.trim()
  if (!query) {
    selectableServers.value = []
    return
  }
  addSearchTimer = setTimeout(async () => {
    addSearchAbortController = new AbortController()
    try {
      const { data } = await api.searchServers({ q: query, limit: 100 }, addSearchAbortController.signal)
      if (addSearch.value.trim() !== query) return
      selectableServers.value = (data.results || []).map(item => ({ ...item.server, _ref: item.ref }))
    } catch (error) {
      if (error?.code === 'ERR_CANCELED') return
      if (addSearch.value.trim() !== query) return
      selectableServers.value = []
    }
  }, 250)
})

function formatTime(t) {
  if (!t) return ''
  return new Date(t).toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

async function connectNode(ref) {
  const outbound = store.outbounds.find(o => o.name === currentOutbound.value)
  const t = outbound?.target
  const normalizedRef = { type: ref.type, index: ref.index, sub: ref.sub || 0 }

  if (t?.targetType === 'group' && currentNodeGroupId(normalizedRef) === t.groupId) {
    await api.setOutboundActiveNode(currentOutbound.value, normalizedRef)
    await Promise.all([store.fetchOutbounds(), store.fetchStatus()])
    return
  }

  // 当前节点不属于已绑定分组时，保持原来的行为：切换为单节点模式。
  if (t?.targetType === 'group') {
    const groupName = store.groups.find(g => g.id === t.groupId)?.name || t.groupId
    if (!confirm(`当前出站已绑定分组「${groupName}」（自动选优）。\n所选节点不在该分组内，点击确认将切换为手动指定此节点。`)) return
  }

  const active = t?.nodeRef
  if (active?.type === normalizedRef.type && active?.index === normalizedRef.index && (active?.sub || 0) === normalizedRef.sub) {
    await api.disconnectOutbound(currentOutbound.value)
  } else {
    await api.connectOutbound(currentOutbound.value, { targetType: 'node', nodeRef: normalizedRef })
  }
  await Promise.all([store.fetchOutbounds(), store.fetchStatus()])
}

function currentNodeGroupId(ref) {
  if (isSearching.value) return ''
  if (activeTab.value.startsWith('group-')) {
    const groupIndex = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[groupIndex]
    const belongsToGroup = (group?.servers || []).some(item =>
      item?.type === ref.type && item?.index === ref.index && (item?.sub || 0) === ref.sub
    )
    return belongsToGroup ? group.id : ''
  }
  if (activeTab.value.startsWith('sub-')) {
    const subscriptionIndex = parseInt(activeTab.value.replace('sub-', ''))
    if (ref.type !== 'sub_server' || ref.sub !== subscriptionIndex) return ''
    return store.subscriptions[subscriptionIndex]?.groupId || ''
  }
  return ''
}

async function pingSingleNode(ref) {
  await pingRefsWithProgress([{ type: ref.type, index: ref.index, sub: ref.sub || 0 }], 'fast')
}

async function pingCurrentTab(mode = 'fast') {
  activePingMode.value = mode
  pinging.value = true
  try {
    let refs = []
    let group = null
    if (activeTab.value.startsWith('sub-')) {
      const si = parseInt(activeTab.value.replace('sub-', ''))
      const sub = store.subscriptions[si]
      refs = getSubscriptionRefs(si)
      group = store.groups.find(g => g.id === sub?.groupId) || null
    } else if (activeTab.value.startsWith('group-')) {
      const gi = parseInt(activeTab.value.replace('group-', ''))
      group = manualGroups.value[gi] || null
      if (group) {
        refs = getGroupPageRefs(group).map(r => ({ type: r.type, index: r.index, sub: r.sub || 0 }))
      }
    }
    if (refs.length) await pingRefsWithProgress(refs, mode)
    // 中途中断时数据只有一半，用它判定切换会挑到"恰好先测完"的节点
    if (refs.length && group && !pingAborted.value) {
      await applyGroupSwitchAfterPing(group, mode)
    }
  } finally {
    pingAbortController.value = null
    pingProgress.value = { total: 0, done: 0, current: '' }
    pingStates.value = {}
    pinging.value = false
    activePingMode.value = ''
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
    refs = getSubscriptionRefs(si)
  } else if (activeTab.value.startsWith('group-')) {
    const gi = parseInt(activeTab.value.replace('group-', ''))
    const group = manualGroups.value[gi]
    refs = getGroupPageRefs(group).map(r => ({ type: r.type, index: r.index, sub: r.sub || 0 }))
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
    await store.fetchNodes(queue)
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
    refs = getSubscriptionRefs(si)
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
    const state = data.state || data
    const wasRunning = ipScoreTask.value.running
    ipScoreTask.value = state
    if (!state.running) {
      stopIPScorePoll()
      ipScoringRefs.value = {}
      await store.fetchNodes(state.refs || [])
      if (wasRunning) {
        const failed = state.failed ? `，${state.failed} 个失败` : ''
        showNotice(`IP 评分完成：成功 ${Math.max(state.done - state.failed, 0)}/${state.total}${failed}。`)
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
  store.invalidateNodeDetails()
  await store.fetchGroups()
  await ensureActiveTabNodes()
}

async function removeFromGroup(groupIndex, ref) {
  await api.removeServerFromGroup(groupIndex, ref)
  // 只刷新分组数据
  await store.fetchGroups()
}

async function refreshSub(si) {
  await api.refreshSubscription(si)
  store.invalidateSubscriptions()
  store.invalidateNodeDetails()
  await store.fetchSubscriptions(false, true)
  await ensureActiveTabNodes()
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
  store.invalidateSubscriptions()
  store.invalidateNodeDetails()
  await store.fetchSubscriptions(false, true)
}

async function deleteSub(si) {
  await api.deleteSubscription(si)
  activeTab.value = 'group-0'
  store.invalidateSubscriptions()
  store.invalidateNodeDetails()
  // 删除订阅需要刷新订阅和分组数据
  await Promise.all([store.fetchSubscriptions(false, true), store.fetchGroups()])
}

async function deleteGroup(gi) {
  const group = manualGroups.value[gi]
  await api.deleteGroup(realGroupIndex(group))
  activeTab.value = 'group-0'
  store.invalidateNodeDetails()
  await store.fetchGroups()
}

async function createGroup() {
  if (!newGroupName.value.trim()) return
  await api.createGroup(newGroupName.value.trim())
  showNewGroup.value = false
  newGroupName.value = ''
  // 只刷新分组数据
  await store.fetchGroups()
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
  // 只刷新分组数据
  await store.fetchGroups()
}

async function openAddToGroup(gi) {
  showAddToGroup.value = gi
  selectedAddIdx.value = -1
  addSearch.value = ''
  selectableServers.value = []
}

async function confirmAddToGroup() {
  if (selectedAddIdx.value < 0) return
  const s = filteredSelectableServers.value[selectedAddIdx.value]
  const group = manualGroups.value[showAddToGroup.value]
  await api.addServerToGroup(realGroupIndex(group), s._ref)
  showAddToGroup.value = -1
  selectedAddIdx.value = -1
  // 只刷新分组数据
  await store.fetchGroups()
}

async function confirmBulkCopy() {
  if (!bulkCopyGroupId.value || !selectedRefs.value.length || bulkCopying.value) return
  bulkCopying.value = true
  try {
    const { data } = await api.batchCopyServersToGroup(selectedRefs.value, bulkCopyGroupId.value)
    store.patchGroupCount(data.groupId, data.groupServerCount)
    showBulkCopyModal.value = false
    bulkCopyGroupId.value = ''
    selectedRefs.value = []
  } catch (error) {
    showNotice('复制失败：' + (error?.response?.data?.error || error?.message || '未知错误'), true)
  } finally {
    bulkCopying.value = false
  }
}

// 移动到分组（从当前分组移除，加入目标分组）
async function confirmBulkMove() {
  if (!bulkMoveGroupId.value || !selectedRefs.value.length || bulkMoving.value) return
  if (!activeTab.value.startsWith('group-')) return
  const gi = parseInt(activeTab.value.replace('group-', ''))
  const group = manualGroups.value[gi]
  if (!group) return
  bulkMoving.value = true
  try {
    const { data } = await api.batchMoveServersToGroup(selectedRefs.value, group.id, bulkMoveGroupId.value)
    store.patchGroupCount(data.sourceGroupId, data.sourceServerCount)
    store.patchGroupCount(data.targetGroupId, data.targetServerCount)
    showBulkMoveModal.value = false
    bulkMoveGroupId.value = ''
    selectedRefs.value = []
    await store.fetchGroupServers(realGroupIndex(group), true, nodePage.value, nodePageSize.value)
  } catch (error) {
    showNotice('移动失败：' + (error?.response?.data?.error || error?.message || '未知错误'), true)
  } finally {
    bulkMoving.value = false
  }
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
  if (!singleTargetGroupId.value || !singleCopyRef.value || singleCopying.value) return
  singleCopying.value = true
  try {
    const { data } = await api.copyServerToGroup(singleCopyRef.value, singleTargetGroupId.value)
    store.patchGroupCount(data.groupId, data.groupServerCount)
    showSingleCopyModal.value = false
    singleCopyRef.value = null
    singleTargetGroupId.value = ''
  } catch (error) {
    showNotice('复制失败：' + (error?.response?.data?.error || error?.message || '未知错误'), true)
  } finally {
    singleCopying.value = false
  }
}

async function confirmSingleMove() {
  if (!singleTargetGroupId.value || !singleMoveRef.value || singleMoving.value) return
  if (!activeTab.value.startsWith('group-')) return
  const gi = parseInt(activeTab.value.replace('group-', ''))
  const group = manualGroups.value[gi]
  if (!group) return
  singleMoving.value = true
  try {
    const { data } = await api.batchMoveServersToGroup([singleMoveRef.value], group.id, singleTargetGroupId.value)
    store.patchGroupCount(data.sourceGroupId, data.sourceServerCount)
    store.patchGroupCount(data.targetGroupId, data.targetServerCount)
    showSingleMoveModal.value = false
    singleMoveRef.value = null
    singleTargetGroupId.value = ''
    await store.fetchGroupServers(realGroupIndex(group), true, nodePage.value, nodePageSize.value)
  } catch (error) {
    showNotice('移动失败：' + (error?.response?.data?.error || error?.message || '未知错误'), true)
  } finally {
    singleMoving.value = false
  }
}
</script>

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
  max-width: 1440px;
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
.mobile-tab-actions { display: none; }

.filter-btn-active {
  border-color: #1677ff;
  color: #1677ff;
  background: #e6f4ff;
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
.mobile-only-action { display: none; }
.content-area {
  flex: 1;
  overflow: hidden;
  padding: 20px 24px;
  max-width: 1440px;
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
.node-pagination { display:flex; align-items:center; justify-content:center; gap:12px; padding:10px 12px; color:#666; font-size:12px; border-top:1px solid #eee; }
.node-page-size { display:flex; align-items:center; gap:6px; white-space:nowrap; }
.node-page-size select { height:28px; padding:0 24px 0 8px; border:1px solid #d9d9d9; border-radius:5px; background:#fff; color:#555; font-size:12px; }
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

@media (max-width: 700px) {
  .home { height: auto; min-height: 100%; }

  .tab-bar {
    display: flex;
    align-items: center;
    padding: 0 8px;
    min-height: 0;
  }

  .tabs-viewport { min-height: 44px; flex: 1; }
  .tab { height: 44px; padding: 0 14px; }
  .tab-scroll-btn { width: 28px; }

  .tab-actions-desktop { display: none; }

  .mobile-tab-actions {
    display: block;
    position: relative;
    flex: 0 0 auto;
    margin-left: 4px;
  }

  .mobile-tab-actions summary {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    border: 1px solid #1677ff;
    border-radius: 7px;
    background: #1677ff;
    color: #fff;
    font-size: 20px;
    line-height: 1;
    cursor: pointer;
    list-style: none;
    user-select: none;
  }

  .mobile-tab-actions summary::-webkit-details-marker { display: none; }

  .mobile-tab-action-menu {
    position: absolute;
    top: 38px;
    right: 0;
    z-index: 30;
    display: grid;
    min-width: 140px;
    padding: 5px;
    border: 1px solid #e8e8e8;
    border-radius: 8px;
    background: #fff;
    box-shadow: 0 8px 24px rgba(0, 0, 0, .14);
  }

  .mobile-tab-action-menu button {
    border: 0;
    border-radius: 5px;
    background: transparent;
    padding: 9px 10px;
    color: #262626;
    font-size: 13px;
    text-align: left;
    white-space: nowrap;
  }

  .mobile-tab-action-menu button:active { background: #f0f5ff; color: #1677ff; }

  .content-area {
    overflow: visible;
    padding: 10px;
  }

  .panel-fill { min-height: 420px; }
  .panel { border-radius: 7px; }

  .info-bar {
    align-items: stretch;
    flex-direction: column;
    padding: 7px 8px;
    gap: 6px;
  }

  .info-bar-left {
    align-items: center;
    flex-wrap: nowrap;
    gap: 6px;
    min-height: 22px;
  }

  .info-url {
    flex: 1;
    min-width: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    line-height: 1.3;
  }

  .info-bar-right {
    display: flex;
    align-items: center;
    flex-wrap: nowrap;
    gap: 6px;
    width: 100%;
    overflow-x: auto;
    overflow-y: hidden;
    scrollbar-width: none;
    overscroll-behavior-x: contain;
    -webkit-overflow-scrolling: touch;
  }

  .info-bar-right::-webkit-scrollbar { display: none; }

  .info-bar-right .action-btn {
    flex: 0 0 auto;
    justify-content: center;
    min-height: 30px;
    padding: 4px 8px;
    font-size: 11px;
    white-space: nowrap;
  }

  .table-scroll {
    overflow-x: hidden;
    overflow-y: auto;
    overscroll-behavior: contain;
  }

  .table-scroll > :first-child { display: none; }

  .bulk-bar {
    align-items: center;
    flex-wrap: nowrap;
    gap: 6px;
    padding: 6px 8px;
    overflow-x: auto;
    overflow-y: hidden;
    scrollbar-width: none;
    overscroll-behavior-x: contain;
    -webkit-overflow-scrolling: touch;
  }

  .bulk-bar::-webkit-scrollbar { display: none; }

  .bulk-count {
    flex: 0 0 auto;
    margin-right: 0;
    padding: 0 4px;
    white-space: nowrap;
  }

  .bulk-bar .action-btn {
    flex: 0 0 auto;
    justify-content: center;
    min-height: 30px;
    padding: 4px 8px;
    font-size: 11px;
    white-space: nowrap;
  }
  .bulk-bar .mobile-only-action { display: inline-flex; }

  .notice-bar { padding: 9px 12px; }

  .node-pagination {
    flex-wrap: wrap;
    gap: 8px;
    padding: 10px;
  }

  .node-pagination span {
    order: -1;
    flex-basis: 100%;
    text-align: center;
  }

  .node-pagination .action-btn {
    flex: 1;
    justify-content: center;
    min-height: 36px;
  }

  .empty-hint { padding: 36px 16px; }

  .sel-row {
    align-items: flex-start;
    flex-wrap: wrap;
    padding: 10px 14px;
  }

  .sel-name { min-width: 0; }
  .sel-addr { flex-basis: 100%; padding-left: 0; overflow-wrap: anywhere; }
}
</style>
