<template>
  <div class="node-filter-panel">
    <label class="node-filter-field node-filter-keyword">
      <span>关键词</span>
      <input
        :value="query"
        placeholder="名称、地址或协议"
        @input="emit('update:query', $event.target.value)"
        @keyup.enter="emit('search')"
      />
    </label>
    <label class="node-filter-field">
      <span>协议</span>
      <input :value="filters.type" placeholder="如 vless" @input="updateField('type', $event.target.value)" />
    </label>
    <label class="node-filter-field">
      <span>地区</span>
      <input :value="filters.geo" placeholder="国家 / 地区 / 城市" @input="updateField('geo', $event.target.value)" />
    </label>
    <label class="node-filter-field">
      <span>来源</span>
      <select :value="filters.source" @change="updateField('source', $event.target.value)">
        <option value="">全部</option>
        <option value="manual">手动节点</option>
        <option value="subscription">订阅节点</option>
      </select>
    </label>
    <label class="node-filter-field">
      <span>IP 类型</span>
      <select :value="filters.ipType" @change="updateField('ipType', $event.target.value)">
        <option value="">全部</option>
        <option value="idc">机房</option>
        <option value="residential">家宽</option>
        <option value="mobile">移动</option>
        <option value="unknown">未知</option>
        <option value="unchecked">未检测</option>
      </select>
    </label>
    <label class="node-filter-field">
      <span>快速连接状态</span>
      <select :value="filters.fastProbe" @change="updateField('fastProbe', $event.target.value)">
        <option value="">全部</option>
        <option value="reachable">可用</option>
        <option value="failed">不通</option>
        <option value="unchecked">未检测</option>
      </select>
    </label>
    <label class="node-filter-field">
      <span>真连状态</span>
      <select :value="filters.probe" @change="updateField('probe', $event.target.value)">
        <option value="">全部</option>
        <option value="reachable">可用</option>
        <option value="failed">不通</option>
        <option value="unchecked">未检测</option>
      </select>
    </label>
    <label class="node-filter-field node-filter-number">
      <span>最大真连延时</span>
      <input :value="filters.maxLatency" type="number" min="1" step="1" placeholder="ms" @input="updateField('maxLatency', $event.target.value)" />
    </label>
    <label class="node-filter-field node-filter-number">
      <span>最低评分</span>
      <input :value="filters.minScore" type="number" min="0" max="100" step="1" placeholder="0-100" @input="updateField('minScore', $event.target.value)" />
    </label>
    <div class="node-filter-actions">
      <span v-if="searching" class="node-filter-status">当前已显示筛选结果，可修改条件后重新搜索</span>
      <button class="action-btn" @click="emit('reset')">清空</button>
      <button v-if="searching" class="action-btn action-btn-danger" @click="emit('clear')">退出筛选</button>
      <button class="action-btn" @click="emit('close')">收起</button>
      <button class="action-btn action-btn-primary" @click="emit('search')">{{ searching ? '重新搜索' : '搜索' }}</button>
    </div>
  </div>
</template>

<script setup>
const props = defineProps({
  query: { type: String, default: '' },
  filters: { type: Object, required: true },
  searching: { type: Boolean, default: false },
})

const emit = defineEmits(['update:query', 'update:filters', 'search', 'reset', 'clear', 'close'])

function updateField(key, value) {
  emit('update:filters', { ...props.filters, [key]: value })
}
</script>

<style scoped>
.node-filter-panel {
  display: flex;
  align-items: flex-end;
  flex-wrap: wrap;
  gap: 10px;
  padding: 12px 16px;
  border-bottom: 1px solid #d6e4ff;
  background: #f5f8ff;
}
.node-filter-field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 130px;
}
.node-filter-keyword { min-width: 190px; }
.node-filter-field > span {
  color: #595959;
  font-size: 11px;
  font-weight: 600;
}
.node-filter-field input,
.node-filter-field select {
  height: 30px;
  box-sizing: border-box;
  padding: 0 9px;
  border: 1px solid #d9d9d9;
  border-radius: 6px;
  outline: none;
  background: #fff;
  color: #262626;
  font-size: 12px;
}
.node-filter-field input:focus,
.node-filter-field select:focus { border-color: #1677ff; }
.node-filter-number { min-width: 110px; }
.node-filter-actions {
  display: flex;
  align-items: center;
  gap: 6px;
  margin-left: auto;
}
.node-filter-status {
  margin-right: 4px;
  color: #8c8c8c;
  font-size: 11px;
  white-space: nowrap;
}
@media (max-width: 700px) {
  .node-filter-panel { align-items: stretch; gap: 8px; padding: 10px; }
  .node-filter-field { flex: 1 1 calc(50% - 8px); min-width: 130px; }
  .node-filter-keyword { flex-basis: 100%; }
  .node-filter-actions { width: 100%; margin-left: 0; justify-content: flex-end; flex-wrap: wrap; }
  .node-filter-status { width: 100%; margin-right: 0; }
}
</style>
