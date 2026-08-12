<template>
  <div :class="['server-row', connected ? 'server-row-connected' : '', selected ? 'server-row-selected' : '']">
    <!-- 多选 checkbox -->
    <input
      type="checkbox"
      class="row-check"
      :checked="selected"
      @pointerdown.stop.prevent="$emit('select-start', refObj, $event)"
      @pointerenter="$emit('select-enter', refObj, $event)"
      @click.stop.prevent
    />

    <!-- 连接状态圆点 -->
    <div :class="['dot', connected ? 'dot-on' : 'dot-off']"></div>

    <!-- 节点名 -->
    <div class="row-name" @click="onNameClick">
      <span :title="server.name || '未命名'">{{ server.name || '未命名' }}</span>
    </div>

    <!-- 地址 -->
    <div class="row-addr"><span :title="`${server.host}:${server.port}`">{{ server.host }}:{{ server.port }}</span></div>

    <!-- IP 地区（评分接口同一次响应附带） -->
    <div class="row-geo" :title="ipGeoTitle">{{ ipGeoText }}</div>

    <!-- 协议 -->
    <div class="row-type">
      <span :class="['tag', `tag-${server.type || 'vmess'}`]">{{ server.type || '?' }}</span>
    </div>

    <!-- IP 类型 -->
    <span :class="['row-iptype', ipTypeClass]" :title="ipTypeTitle">{{ ipTypeText }}</span>

    <!-- IP 评分 -->
    <span :class="['row-ipscore', ipScoreClass]" :title="ipScoreTitle">{{ ipScoreText }}</span>

    <!-- 探测状态/延迟 -->
    <span :class="['row-lat', pingStateClass, latClass(server.latency)]">{{ pingStateText }}</span>

    <!-- 操作按钮 -->
    <div class="row-ops">
      <!-- 连接按钮 -->
      <button
        :class="['row-btn', 'row-btn-connect', connected ? 'row-btn-connected' : '']"
        :title="connectionButtonTitle"
        @click="$emit('connect', refObj)"
      >
        {{ connected ? '已连接' : '连接' }}
      </button>
      
      <!-- 分享按钮 -->
      <button class="row-btn" title="分享" @click="$emit('share', refObj, server)">
        🔗<span class="row-btn-label">分享</span>
      </button>
      
      <!-- 复制到分组 -->
      <button class="row-btn" title="复制到分组" @click="$emit('copy-to-group', refObj)">
        📋<span class="row-btn-label">复制</span>
      </button>
      
      <!-- 移动到分组（仅手动分组节点） -->
      <button v-if="removable" class="row-btn" title="移动到分组" @click="$emit('move-to-group', refObj)">
        ✂<span class="row-btn-label">移动</span>
      </button>
      
      <!-- 编辑按钮（仅手动节点） -->
      <button v-if="refObj.type === 'server'" class="row-btn" title="编辑" @click="$emit('edit', refObj, server)">
        ✏<span class="row-btn-label">编辑</span>
      </button>
      
      <!-- 删除按钮（仅可删除的节点） -->
      <button v-if="removable" class="row-btn row-btn-danger" title="移除" @click="$emit('remove', refObj)">
        ✕<span class="row-btn-label">移除</span>
      </button>
    </div>
  </div>
</template>

<script setup>
import { computed, ref } from 'vue'

const props = defineProps({
  server:    { type: Object, required: true },
  refObj:    { type: Object, required: true },
  outbounds: { type: Array, default: () => [] },
  removable: { type: Boolean, default: false },
  selected:  { type: Boolean, default: false },
  currentOutbound: { type: String, default: 'proxy' },
  pingState: { type: Object, default: null },
  ipTypeState: { type: Object, default: null },
  ipScoreRunning: { type: Boolean, default: false },
})
const emit = defineEmits([
  'connect',
  'remove',
  'edit',
  'share',
  'select',
  'select-start',
  'select-enter',
  'copy-to-group',
  'move-to-group',
])

// 单元格文字可选中后，划词的落点也会触发 click。此时若照常切换行选中，
// 用户复制节点名的动作就会顺手把行选上，所以有未折叠的选区时直接放过。
function onNameClick(event) {
  const selection = window.getSelection?.()
  if (selection && !selection.isCollapsed && selection.toString().trim()) return
  emit('select', props.refObj, !props.selected, event)
}

const connected = computed(() => {
  const outbound = props.outbounds.find(o => o.name === props.currentOutbound)
  if (!outbound?.target || !props.refObj) return false
  const t = outbound.target
  if (t.targetType === 'node' && t.nodeRef) {
    return t.nodeRef.type === props.refObj.type &&
      t.nodeRef.index === props.refObj.index &&
      (t.nodeRef.sub || 0) === (props.refObj.sub || 0)
  }
  if (t.targetType === 'group' && t.activeNodeRef) {
    return t.activeNodeRef.type === props.refObj.type &&
      t.activeNodeRef.index === props.refObj.index &&
      (t.activeNodeRef.sub || 0) === (props.refObj.sub || 0)
  }
  return false
})

const connectionButtonTitle = computed(() => {
  if (!connected.value) return '连接此节点'
  const outbound = props.outbounds.find(o => o.name === props.currentOutbound)
  return outbound?.target?.targetType === 'group' ? '当前分组节点' : '断开连接'
})

function latText(lat) {
  if (!lat || lat < 0) return '-'
  return lat + 'ms'
}
function latClass(lat) {
  if (!lat || lat < 0) return 'lat-none'
  if (lat < 200) return 'lat-good'
  if (lat < 500) return 'lat-ok'
  return 'lat-bad'
}

const pingStateText = computed(() => {
  if (!props.pingState) return latText(props.server.latency)
  if (props.pingState.status === 'pending') return '等待中'
  if (props.pingState.status === 'running') return '测速中'
  if (typeof props.pingState.latency === 'number') {
    if (props.pingState.latency < 0) return '-1'
    return `${props.pingState.latency}ms`
  }
  return latText(props.server.latency)
})

const IP_TYPE_LABELS = {
  idc: '机房',
  residential: '家宽',
  mobile: '移动',
  unknown: '未知',
}

const ipTypeText = computed(() => {
  if (props.ipTypeState?.status === 'running') return '检测中'
  return IP_TYPE_LABELS[props.server.ipType] || '-'
})

const ipTypeClass = computed(() => {
  if (props.ipTypeState?.status === 'running') return 'iptype-running'
  return `iptype-${props.server.ipType || 'empty'}`
})

const ipTypeTitle = computed(() => {
  if (props.ipTypeState?.status === 'running') return '正在检测 IP 类型'
  if (!props.server.ipType) return 'IP 类型未检测'
  const parts = [IP_TYPE_LABELS[props.server.ipType] || props.server.ipType]
  if (props.server.ipTypeAddr) parts.push(props.server.ipTypeAddr)
  if (props.server.ipTypeIsp) parts.push(props.server.ipTypeIsp)
  if (props.server.ipTypeCheckedAt) {
    parts.push(`检测于 ${new Date(props.server.ipTypeCheckedAt * 1000).toLocaleString()}`)
  }
  return parts.join(' · ')
})

const ipScoreText = computed(() => {
  if (props.ipScoreRunning) return '…'
  const s = props.server.ipScore
  if (s === null || s === undefined) return '-'
  return props.server.ipScoreGrade ? `${s} ${props.server.ipScoreGrade}` : String(s)
})

const ipScoreClass = computed(() => {
  if (props.ipScoreRunning) return 'ipscore-running'
  const s = props.server.ipScore
  if (s === null || s === undefined) return 'ipscore-empty'
  if (s >= 80) return 'ipscore-good'
  if (s >= 60) return 'ipscore-fair'
  if (s >= 40) return 'ipscore-warn'
  return 'ipscore-bad'
})

const ipScoreTitle = computed(() => {
  if (props.ipScoreRunning) return '正在查询 IP 评分'
  const s = props.server.ipScore
  if (s === null || s === undefined) return 'IP 评分未查询'
  const parts = [`纯净度 ${s}/100`]
  if (props.server.ipScoreGrade) parts.push(`等级 ${props.server.ipScoreGrade}`)
  if (props.server.ipScoreRisk) parts.push(props.server.ipScoreRisk)
  if (props.server.ipScoreProvider) parts.push(props.server.ipScoreProvider)
  if (ipGeoText.value !== '-') parts.push(`地区 ${ipGeoText.value}`)
  if (props.server.ipScoreAt) {
    parts.push(`查询于 ${new Date(props.server.ipScoreAt * 1000).toLocaleString()}`)
  }
  return parts.join(' · ')
})

function countryFlag(code) {
  const normalized = String(code || '').trim().toUpperCase()
  if (!/^[A-Z]{2}$/.test(normalized)) return ''
  return String.fromCodePoint(...[...normalized].map(char => 127397 + char.charCodeAt(0)))
}

const ipGeoParts = computed(() => {
  const parts = [props.server.ipGeoCountry, props.server.ipGeoRegion, props.server.ipGeoCity]
    .map(value => String(value || '').trim())
    .filter(Boolean)
  return parts.filter((value, index) => parts.indexOf(value) === index)
})

const ipGeoText = computed(() => {
  if (!ipGeoParts.value.length) return '-'
  const flag = countryFlag(props.server.ipGeoCountryCode)
  return `${flag ? flag + ' ' : ''}${ipGeoParts.value.join(' · ')}`
})

const ipGeoTitle = computed(() => {
  if (!ipGeoParts.value.length) return '地区信息将在获取 IP 评分后显示'
  const code = String(props.server.ipGeoCountryCode || '').trim().toUpperCase()
  return code ? `${ipGeoText.value} (${code})` : ipGeoText.value
})

const pingStateClass = computed(() => {
  if (!props.pingState) return ''
  if (props.pingState.status === 'pending') return 'lat-pending'
  if (props.pingState.status === 'running') return 'lat-running'
  if (typeof props.pingState.latency === 'number' && props.pingState.latency < 0) return 'lat-fail'
  return ''
})
</script>

<style scoped>
.server-row {
  display: flex;
  align-items: stretch;
  gap: 0;
  padding: 0 16px;
  border-bottom: 1px solid #f0f0f0;
  transition: background .1s;
  user-select: none;
  min-width: max-content;
}
.server-row:last-child { border-bottom: none; }
.server-row:hover { background: #fafafa; }
.server-row-connected { background: #f6fffa; }
.server-row-connected:hover { background: #edfaf3; }
.server-row-selected { background: #e6f0ff !important; }

/* 每个单元格左侧一条竖线，与表头的分隔线对齐 */
.server-row > *:not(.row-check):not(.dot) {
  padding: 8px;
  border-left: 1px solid #f2f2f2;
  display: flex;
  align-items: center;
}

/* 文字单元格允许划词，方便右键复制节点名/地址 */
.row-name, .row-addr, .row-geo, .row-type, .row-iptype, .row-ipscore, .row-lat {
  user-select: text;
  -webkit-user-select: text;
}

.row-check {
  width: 14px; height: 14px;
  flex: 0 0 14px;
  margin: auto 10px auto 0;
  cursor: pointer;
  accent-color: #1677ff;
}

.dot {
  width: 7px; height: 7px;
  border-radius: 50%;
  flex: 0 0 7px;
  margin: auto 10px auto 0;
}
.dot-on  { background: #52c41a; box-shadow: 0 0 4px rgba(82,196,26,.7); }
.dot-off { background: #d9d9d9; }

/* 各列宽度取自父级 CSS 变量，与 ServerListHead 的同名变量保持一致 */
.row-name {
  flex: 0 0 var(--col-name);
  width: var(--col-name);
  font-size: 13px;
  color: #262626;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  cursor: pointer;
  border-left: none !important;
}
.row-name > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.server-row-connected .row-name { color: #389e0d; font-weight: 500; }

.row-addr {
  flex: 0 0 var(--col-addr);
  width: var(--col-addr);
  font-size: 11px;
  color: #8c8c8c;
  white-space: nowrap;
  overflow: hidden;
}
.row-addr > span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.row-type {
  flex: 0 0 var(--col-type);
  width: var(--col-type);
  overflow: hidden;
}
.row-geo {
  flex: 0 0 var(--col-geo);
  width: var(--col-geo);
  font-size: 11px;
  color: #595959;
  cursor: default;
}

.tag {
  display: inline-block;
  max-width: 100%;
  padding: 2px 7px;
  border-radius: 3px;
  font-size: 11px;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  vertical-align: middle;
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

.row-lat {
  flex: 0 0 var(--col-latency);
  width: var(--col-latency);
  font-size: 12px;
  font-weight: 600;
  text-align: right;
  white-space: nowrap;
  overflow: hidden;
}
.row-iptype {
  flex: 0 0 var(--col-iptype);
  width: var(--col-iptype);
  font-size: 11px;
  font-weight: 600;
  text-align: center;
  white-space: nowrap;
  overflow: hidden;
  cursor: default;
}
.iptype-idc { color: #3273dc; }
.iptype-residential { color: #257953; }
.iptype-mobile { color: #b7791f; }
.iptype-unknown { color: #bfbfbf; font-weight: 400; }
.iptype-empty { color: #d9d9d9; font-weight: 400; }
.iptype-running { color: #1677ff; }

.row-ipscore {
  flex: 0 0 var(--col-ipscore);
  width: var(--col-ipscore);
  font-size: 11px;
  font-weight: 600;
  text-align: center;
  white-space: nowrap;
  overflow: hidden;
  cursor: default;
}
.ipscore-good  { color: #237804; }
.ipscore-fair  { color: #52c41a; }
.ipscore-warn  { color: #faad14; }
.ipscore-bad   { color: #ff4d4f; }
.ipscore-empty { color: #d9d9d9; font-weight: 400; }
.ipscore-running { color: #1677ff; }

.lat-good { color: #52c41a; }
.lat-ok   { color: #faad14; }
.lat-bad  { color: #ff4d4f; }
.lat-none { color: #bfbfbf; font-weight: 400; }
.lat-pending { color: #8c8c8c; font-weight: 500; }
.lat-running { color: #1677ff; font-weight: 600; }
.lat-fail { color: #ff4d4f; font-weight: 600; }

.row-ops {
  display: flex;
  gap: 4px;
  flex: 0 0 var(--col-ops);
  width: var(--col-ops);
  align-items: center;
  justify-content: flex-start;
}

.row-btn {
  background: none;
  border: 1px solid #e8e8e8;
  border-radius: 4px;
  padding: 3px 8px;
  font-size: 11px;
  cursor: pointer;
  color: #8c8c8c;
  transition: all .1s;
  line-height: 1.4;
  white-space: nowrap;
  flex-shrink: 0;
}
.row-btn-label { display: none; }
.row-btn:hover { background: #f5f5f5; border-color: #bfbfbf; color: #595959; }

.row-btn-connect {
  border-color: #1677ff;
  color: #1677ff;
  font-weight: 500;
}
.row-btn-connect:hover { background: #e6f0ff; }
.row-btn-connected {
  background: #52c41a;
  border-color: #52c41a;
  color: #fff;
}
.row-btn-connected:hover { background: #389e0d; border-color: #389e0d; }

.row-btn-danger {
  border-color: #ff4d4f;
  color: #ff4d4f;
}
.row-btn-danger:hover { background: #fff1f0; }

@media (max-width: 700px) {
  .server-row {
    display: grid;
    grid-template-columns: 20px 12px minmax(0, 1fr) auto;
    align-items: center;
    gap: 6px 8px;
    min-width: 0;
    width: 100%;
    padding: 12px;
    user-select: auto;
  }

  .server-row > *:not(.row-check):not(.dot) {
    border-left: 0;
    padding: 0;
  }

  .row-check {
    grid-column: 1;
    grid-row: 1;
    width: 18px;
    height: 18px;
    flex: none;
    margin: 0;
  }

  .dot {
    grid-column: 2;
    grid-row: 1;
    margin: 0;
  }

  .row-name {
    grid-column: 3;
    grid-row: 1;
    width: auto;
    min-width: 0;
    font-size: 14px;
    font-weight: 600;
  }

  .row-lat {
    grid-column: 4;
    grid-row: 1;
    width: auto;
    min-width: 48px;
    justify-content: flex-end;
    font-size: 12px;
  }

  .row-addr {
    grid-column: 3 / 5;
    grid-row: 2;
    width: auto;
    min-width: 0;
    font-size: 12px;
  }

  .row-geo {
    grid-column: 3 / 5;
    grid-row: 3;
    width: auto;
    min-width: 0;
    color: #8c8c8c;
    font-size: 11px;
  }

  .row-type,
  .row-iptype,
  .row-ipscore {
    grid-row: 4;
    width: auto;
    min-width: 0;
    justify-content: center;
    padding: 4px 7px !important;
    border-radius: 5px;
    background: #fafafa;
  }

  .row-type { grid-column: 1 / 3; }
  .row-iptype { grid-column: 3; }
  .row-ipscore { grid-column: 4; }

  .row-ops {
    display: none !important;
  }
}
</style>
