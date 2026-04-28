<template>
  <div :class="['server-row', connected ? 'server-row-connected' : '', selected ? 'server-row-selected' : '']">
    <!-- 多选 checkbox -->
    <input
      type="checkbox"
      class="row-check"
      :checked="selected"
      @change="$emit('select', refObj, $event.target.checked)"
      @click.stop
    />

    <!-- 连接状态圆点 -->
    <div :class="['dot', connected ? 'dot-on' : 'dot-off']"></div>

    <!-- 节点名 -->
    <div class="row-name" @click="$emit('select', refObj, !selected)">
      {{ server.name || '未命名' }}
    </div>

    <!-- 地址 -->
    <div class="row-addr">{{ server.host }}:{{ server.port }}</div>

    <!-- 协议 -->
    <span :class="['tag', `tag-${server.type || 'vmess'}`]">{{ server.type || '?' }}</span>

    <!-- 延迟 -->
    <span :class="['row-lat', latClass(server.latency)]">{{ latText(server.latency) }}</span>

    <!-- 操作按钮 -->
    <div class="row-ops">
      <!-- 连接按钮 -->
      <button
        :class="['row-btn', 'row-btn-connect', connected ? 'row-btn-connected' : '']"
        :title="connected ? '断开连接' : '连接此节点'"
        @click="$emit('connect', refObj)"
      >
        {{ connected ? '已连接' : '连接' }}
      </button>
      
      <!-- 分享按钮 -->
      <button class="row-btn" title="分享" @click="$emit('share', refObj, server)">🔗</button>
      
      <!-- 复制到分组 -->
      <button class="row-btn" title="复制到分组" @click="$emit('copy-to-group', refObj)">📋</button>
      
      <!-- 移动到分组（仅手动分组节点） -->
      <button v-if="removable" class="row-btn" title="移动到分组" @click="$emit('move-to-group', refObj)">✂</button>
      
      <!-- 编辑按钮（仅手动节点） -->
      <button v-if="refObj.type === 'server'" class="row-btn" title="编辑" @click="$emit('edit', refObj, server)">✏</button>
      
      <!-- 删除按钮（仅可删除的节点） -->
      <button v-if="removable" class="row-btn row-btn-danger" title="移除" @click="$emit('remove', refObj)">✕</button>
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
})
defineEmits(['connect', 'remove', 'edit', 'share', 'select', 'copy-to-group', 'move-to-group'])

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
</script>

<style scoped>
.server-row {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 9px 16px;
  border-bottom: 1px solid #f5f5f5;
  transition: background .1s;
  user-select: none;
}
.server-row:last-child { border-bottom: none; }
.server-row:hover { background: #fafafa; }
.server-row-connected { background: #f6fffa; }
.server-row-connected:hover { background: #edfaf3; }
.server-row-selected { background: #e6f0ff !important; }

.row-check {
  width: 14px; height: 14px;
  cursor: pointer;
  flex-shrink: 0;
  accent-color: #1677ff;
}

.dot {
  width: 7px; height: 7px;
  border-radius: 50%;
  flex-shrink: 0;
}
.dot-on  { background: #52c41a; box-shadow: 0 0 4px rgba(82,196,26,.7); }
.dot-off { background: #d9d9d9; }

.row-name {
  flex: 1.5;
  font-size: 13px;
  color: #262626;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  cursor: pointer;
  min-width: 0;
}
.server-row-connected .row-name { color: #389e0d; font-weight: 500; }

.row-addr {
  flex: 2;
  font-size: 11px;
  color: #bfbfbf;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
}

.tag {
  flex-shrink: 0;
  display: inline-block;
  padding: 2px 7px;
  border-radius: 3px;
  font-size: 11px;
  font-weight: 600;
  margin-right: 16px;
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
  flex-shrink: 0;
  font-size: 12px;
  font-weight: 600;
  min-width: 52px;
  text-align: right;
  white-space: nowrap;
  margin-right: 16px;
}
.lat-good { color: #52c41a; }
.lat-ok   { color: #faad14; }
.lat-bad  { color: #ff4d4f; }
.lat-none { color: #bfbfbf; font-weight: 400; }

.row-ops { 
  display: flex; 
  gap: 4px; 
  flex-shrink: 0; 
  align-items: center;
  margin-left: auto;
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
</style>
