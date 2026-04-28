<template>
  <div class="modal-overlay" @click.self="$emit('close')">
    <div class="modal-box">
      <div class="modal-header">
        <span class="modal-title">导入</span>
        <span class="modal-close" @click="$emit('close')">✕</span>
      </div>
      <div class="modal-body">
        <p style="color:#7a7a7a; font-size:12px; margin-bottom:12px">
          支持节点链接（vmess/vless/ss/trojan/...）和订阅链接（http/https），每行一个，可混合粘贴。
          订阅链接会自动创建分组。
        </p>
        <div class="form-group">
          <label class="form-label">链接内容</label>
          <textarea
            class="input"
            v-model="content"
            rows="8"
            placeholder="vmess://...&#10;vless://...&#10;https://your-subscription-url.com/..."
            style="resize:vertical; font-family:monospace; font-size:12px"
            autofocus
          ></textarea>
        </div>
        <div class="form-group" v-if="hasNodeLinks">
          <label class="form-label">节点加入分组（可选）</label>
          <select class="input" v-model="groupId">
            <option value="">不加入分组</option>
            <option v-for="g in manualGroups" :key="g.id" :value="g.id">{{ g.name }}</option>
          </select>
        </div>
        <div v-if="result" :class="['result-box', result.error ? 'result-error' : 'result-ok']">
          <template v-if="result.error">{{ result.error }}</template>
          <template v-else>
            导入完成：
            <span v-if="result.subCount">{{ result.subCount }} 个订阅（后台拉取中）</span>
            <span v-if="result.nodeCount">{{ result.nodeCount }} 个节点</span>
          </template>
        </div>
      </div>
      <div class="modal-footer">
        <button class="btn btn-light" @click="$emit('close')">取消</button>
        <button class="btn btn-primary" :disabled="!content.trim() || loading" @click="doImport">
          {{ loading ? '导入中...' : '导入' }}
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'
import { useProxyStore } from '../stores/proxy'
import { api } from '../api'

const emit = defineEmits(['close', 'done'])
const store = useProxyStore()
const content = ref('')
const groupId = ref('')
const loading = ref(false)
const result = ref(null)

// 打开时立即刷新分组列表，确保和主页面同步
onMounted(() => store.fetchAll())

const hasNodeLinks = computed(() => {
  const nodePrefixes = [
    'vmess://', 'vless://', 'ss://', 'ssr://', 'trojan://', 'trojan-go://',
    'hysteria://', 'hysteria2://', 'hy2://', 'tuic://', 'juicity://',
    'wireguard://', 'socks://', 'socks5://', 'socks4://',
    'naive+https://', 'naive+http://',
  ]
  return content.value.split('\n').some(line => {
    const l = line.trim().toLowerCase()
    if (nodePrefixes.some(p => l.startsWith(p))) return true
    if (l.startsWith('http://') || l.startsWith('https://')) {
      const afterScheme = l.replace(/^https?:\/\//, '')
      const slashIdx = afterScheme.indexOf('/')
      const hostPart = slashIdx >= 0 ? afterScheme.slice(0, slashIdx) : afterScheme
      return hostPart.includes('@')
    }
    return false
  })
})

// 只显示手动创建的分组（非订阅自动创建）
const manualGroups = computed(() => store.groups.filter(g => !g.fromSub))

async function doImport() {
  if (!content.value.trim()) return
  loading.value = true
  result.value = null
  try {
    const { data } = await api.import({ content: content.value, groupId: groupId.value })
    result.value = data
    if (data.imported > 0) {
      setTimeout(() => emit('done'), 800)
    }
  } catch (e) {
    result.value = { error: e?.response?.data?.error || '导入失败' }
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.result-box {
  padding: 10px 14px;
  border-radius: 4px;
  font-size: 13px;
  margin-top: 4px;
}
.result-ok  { background: #effaf5; color: #257953; border: 1px solid #48c774; }
.result-error { background: #fff5f7; color: #cc0f35; border: 1px solid #f14668; }
</style>
