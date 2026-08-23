<!-- 节点右键/操作菜单：编辑、分享（二维码）、复制到分组 -->
<template>
  <!-- 编辑弹窗 -->
  <div class="modal-overlay" v-if="mode === 'edit'" @mousedown.self="close">
    <div class="modal-box" style="width:480px">
      <div class="modal-header">
        <span class="modal-title">编辑节点</span>
        <span class="modal-close" @click="close">✕</span>
      </div>
      <div class="modal-body">
        <div class="form-group">
          <label class="form-label">节点名称</label>
          <input class="input" v-model="editName" placeholder="节点名称" />
        </div>
        <div class="form-group">
          <label class="form-label">节点链接</label>
          <textarea class="input" v-model="editLink" rows="4"
            :disabled="editLoading"
            :placeholder="editLoading ? '正在加载节点链接…' : '节点链接'"
            style="font-family:monospace; font-size:12px; resize:vertical" />
          <div v-if="editError" class="form-error">{{ editError }}</div>
        </div>
        <div class="form-group">
          <label class="form-label">前置代理</label>
          <select class="input" v-model="editFrontProxy">
            <option value="">无前置（直连）</option>
            <option v-for="ob in store.outbounds" :key="ob.name" :value="ob.name">
              {{ ob.name }}
            </option>
          </select>
          <div class="form-hint">前置代理用于链式代理，流量会先经过指定出站再访问此节点</div>
        </div>
      </div>
      <div class="modal-footer">
        <button class="btn btn-light" @click="close">取消</button>
        <button class="btn btn-primary" :disabled="editLoading || !!editError" @click="saveEdit">保存</button>
      </div>
    </div>
  </div>

  <!-- 分享二维码弹窗 -->
  <div class="modal-overlay" v-if="mode === 'share'" @mousedown.self="close">
    <div class="modal-box" style="width:360px; text-align:center">
      <div class="modal-header">
        <span class="modal-title">分享节点</span>
        <span class="modal-close" @click="close">✕</span>
      </div>
      <div class="modal-body" style="display:flex; flex-direction:column; align-items:center; gap:16px">
        <canvas ref="qrCanvas" style="border-radius:8px; border:1px solid #e8e8e8"></canvas>
        <div style="font-size:12px; color:#8c8c8c; word-break:break-all; max-width:300px">
          {{ shareLink }}
        </div>
        <button class="btn btn-light btn-sm" @click="copyLink">
          {{ copied ? '✓ 已复制' : '复制链接' }}
        </button>
      </div>
      <div class="modal-footer" style="justify-content:center">
        <button class="btn btn-primary" @click="close">关闭</button>
      </div>
    </div>
  </div>

  <!-- 复制到分组弹窗 -->
  <div class="modal-overlay" v-if="mode === 'copy'" @mousedown.self="close">
    <div class="modal-box" style="width:360px">
      <div class="modal-header">
        <span class="modal-title">复制到分组</span>
        <span class="modal-close" @click="close">✕</span>
      </div>
      <div class="modal-body">
        <div class="form-group">
          <label class="form-label">选择目标分组</label>
          <select class="input" v-model="targetGroupId">
            <option value="">-- 请选择 --</option>
            <option v-for="g in manualGroups" :key="g.id" :value="g.id">{{ g.name }}</option>
          </select>
        </div>
      </div>
      <div class="modal-footer">
        <button class="btn btn-light" @click="close">取消</button>
        <button class="btn btn-primary" :disabled="!targetGroupId" @click="doCopy">复制</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, nextTick } from 'vue'
import QRCode from 'qrcode'
import { useProxyStore } from '../stores/proxy'
import { api } from '../api'

const props = defineProps({
  mode: { type: String, default: '' },  // 'edit' | 'share' | 'copy' | ''
  refObj: { type: Object, default: null },
  server: { type: Object, default: null },
})
const emit = defineEmits(['close', 'updated'])

const store = useProxyStore()
const editName = ref('')
const editLink = ref('')
const editFrontProxy = ref('')
const editLoading = ref(false)
const editError = ref('')
const shareLink = ref('')
const qrCanvas = ref(null)
const copied = ref(false)
const targetGroupId = ref('')

const manualGroups = computed(() => store.groups.filter(g => !g.fromSub))

// 初始化编辑表单
watch(() => props.mode, async (m) => {
  if (m === 'edit' && props.server && props.refObj) {
    editName.value = props.server.name || ''
    editFrontProxy.value = props.server.frontProxy || ''
    editLink.value = ''
    editError.value = ''
    editLoading.value = true
    try {
      const { data } = await api.getServerLink(props.refObj.type, props.refObj.index, props.refObj.sub || 0, true)
      editLink.value = data.link || ''
    } catch (error) {
      editError.value = '节点链接加载失败：' + (error?.response?.data?.error || error?.message || '未知错误')
    } finally {
      editLoading.value = false
    }
  }
  if (m === 'share' && props.refObj) {
    const { data } = await api.getServerLink(props.refObj.type, props.refObj.index, props.refObj.sub || 0)
    shareLink.value = data.link || ''
    await nextTick()
    if (qrCanvas.value && shareLink.value) {
      QRCode.toCanvas(qrCanvas.value, shareLink.value, {
        width: 240,
        margin: 2,
        color: { dark: '#000', light: '#fff' }
      })
    }
  }
  if (m === 'copy') {
    targetGroupId.value = ''
    // 只刷新分组列表，不需要拉取所有数据
    await store.fetchGroups()
  }
}, { immediate: true })

function close() {
  emit('close')
}

async function saveEdit() {
  if (!props.refObj) return
  const { data } = await api.editNode(props.refObj, {
    name: editName.value,
    link: editLink.value,
    frontProxy: editFrontProxy.value,
  })
  // 只更新这个节点的数据，不重新拉取所有内容
  if (data.server) {
    store.patchNode(props.refObj, data.server)
  }
  emit('updated', props.refObj, data.server)
  close()
}

async function copyLink() {
  if (!shareLink.value) return
  await navigator.clipboard.writeText(shareLink.value)
  copied.value = true
  setTimeout(() => { copied.value = false }, 2000)
}

async function doCopy() {
  if (!targetGroupId.value || !props.refObj) return
  await api.copyServerToGroup(props.refObj, targetGroupId.value)
  // 只刷新分组数据，不需要拉取订阅和服务器
  await store.fetchGroups()
  emit('updated')
  close()
}
</script>

<style scoped>
.form-hint {
  font-size: 11px;
  color: #8c8c8c;
  margin-top: 4px;
  line-height: 1.5;
}
.form-error {
  margin-top: 4px;
  color: #ff4d4f;
  font-size: 11px;
  line-height: 1.5;
}
</style>

