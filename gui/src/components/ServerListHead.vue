<template>
  <div class="server-list-head">
    <span class="head-gap head-gap-check"></span>
    <span class="head-gap head-gap-status"></span>
    <template v-for="col in columns" :key="col.key">
      <span :class="['head-col', 'head-col-' + col.key]">
        <button
          v-if="col.sortable"
          class="head-label head-label-btn"
          :title="'按' + col.label + '排序（点击后固定顺序）'"
          @click="emit('sort', col.key)"
        >
          {{ col.label }}<span class="head-arrow">{{ sortArrow(col.key) }}</span>
        </button>
        <span v-else class="head-label">{{ col.label }}</span>
        <span
          v-if="!col.noResizer"
          class="head-resizer"
          title="拖动调整列宽，双击恢复默认"
          @mousedown="startResize(col.key, $event)"
          @dblclick="emit('reset-width', col.key)"
        ></span>
      </span>
    </template>
  </div>
</template>

<script setup>
import { onBeforeUnmount } from 'vue'

const props = defineProps({
  columns: { type: Array, required: true },
  sortKey: { type: String, default: '' },
  sortDir: { type: String, default: '' },
})

const emit = defineEmits(['sort', 'resize', 'resize-end', 'reset-width'])

function sortArrow(key) {
  if (props.sortKey !== key) return ''
  return props.sortDir === 'desc' ? ' ↓' : ' ↑'
}

let dragging = null

function startResize(key, event) {
  event.preventDefault()
  dragging = { key, startX: event.clientX }
  window.addEventListener('mousemove', onDragMove)
  window.addEventListener('mouseup', stopResize)
  document.body.style.userSelect = 'none'
  document.body.style.cursor = 'col-resize'
}

function onDragMove(event) {
  if (!dragging) return
  emit('resize', dragging.key, event.clientX - dragging.startX)
}

function stopResize() {
  if (!dragging) return
  dragging = null
  window.removeEventListener('mousemove', onDragMove)
  window.removeEventListener('mouseup', stopResize)
  document.body.style.userSelect = ''
  document.body.style.cursor = ''
  emit('resize-end')
}

onBeforeUnmount(stopResize)
</script>

<style scoped>
.server-list-head {
  display: flex;
  align-items: stretch;
  gap: 0;
  padding: 0 16px;
  border-bottom: 2px solid #e8e8e8;
  background: #fafafa;
  flex-shrink: 0;
  min-width: max-content;
}

/* 与行首的状态点、复选框对齐 */
.head-gap-status { flex: 0 0 7px; margin-right: 10px; }
.head-gap-check { flex: 0 0 14px; margin-right: 10px; }

.head-col {
  position: relative;
  display: flex;
  align-items: center;
  padding: 7px 8px;
  font-size: 11px;
  color: #8c8c8c;
  font-weight: 600;
  white-space: nowrap;
  overflow: hidden;
  /* 列分隔线：让表头看起来是真正的表格而不是一排文字 */
  border-left: 1px solid #ebebeb;
}
.head-col:first-of-type { border-left: none; }

.head-col-name { flex: 0 0 var(--col-name); width: var(--col-name); }
.head-col-addr { flex: 0 0 var(--col-addr); width: var(--col-addr); }
.head-col-geo { flex: 0 0 var(--col-geo); width: var(--col-geo); }
.head-col-type { flex: 0 0 var(--col-type); width: var(--col-type); }
.head-col-iptype {
  flex: 0 0 var(--col-iptype);
  width: var(--col-iptype);
  justify-content: center;
}
.head-col-ipscore {
  flex: 0 0 var(--col-ipscore);
  width: var(--col-ipscore);
  justify-content: center;
}
.head-col-fastLatency {
  flex: 0 0 var(--col-fastLatency);
  width: var(--col-fastLatency);
  justify-content: flex-end;
}
.head-col-latency {
  flex: 0 0 var(--col-latency);
  width: var(--col-latency);
  justify-content: flex-end;
}
.head-col-ops {
  flex: 0 0 var(--col-ops);
  width: var(--col-ops);
  justify-content: flex-start;
}

.head-label {
  overflow: hidden;
  text-overflow: ellipsis;
}
.head-label-btn {
  border: none;
  background: transparent;
  padding: 0;
  font: inherit;
  color: #8c8c8c;
  font-weight: 600;
  cursor: pointer;
}
.head-label-btn:hover { color: #1677ff; }
.head-arrow { color: #1677ff; }

.head-resizer {
  position: absolute;
  top: 0;
  bottom: 0;
  right: -4px;
  width: 9px;
  cursor: col-resize;
  z-index: 1;
}
.head-resizer:hover {
  background: linear-gradient(to right, transparent 3px, #1677ff 3px, #1677ff 5px, transparent 5px);
}
</style>
