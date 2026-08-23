import axios from 'axios'

const http = axios.create({
  baseURL: '/api',
  withCredentials: true,
})

let unauthorizedHandler = null

http.interceptors.response.use(
  (response) => response,
  (error) => {
    if (error?.response?.status === 401) {
      unauthorizedHandler?.(error)
    }
    return Promise.reject(error)
  }
)

export function setUnauthorizedHandler(handler) {
  unauthorizedHandler = handler
}

export const api = {
  // 认证
  login: (username, password) => http.post('/auth/login', { username, password }),
  logout: () => http.post('/auth/logout'),
  me: () => http.get('/auth/me'),
  changePassword: (data) => http.post('/auth/change-password', data),

  // 状态
  getBootstrap: () => http.get('/bootstrap'),
  getStatus: () => http.get('/status'),
  startProxy: () => http.post('/start'),
  stopProxy: () => http.post('/stop'),

  // 节点
  getServers: (view = 'list') => http.get('/servers', { params: { view } }),
  queryServers: (refs) => http.post('/servers/query', { refs }),
  searchServers: (params = {}, signal) => http.get('/servers/search', { params, signal }),
  getServerBySource: (prefix) => http.get('/servers/by-source', { params: { prefix } }),
  import: (data) => http.post('/import', data),
  importServers: (links, groupId) => http.post('/servers', { links, groupId }),
  deleteServers: (indexes) => http.delete('/servers', { data: { indexes } }),
  editServer: (index, data) => http.put(`/servers/${index}`, data),
  editNode: (ref, data) => http.put('/server', { ref, ...data }),
  getServerLink: (type, index, sub = 0, raw = false) => http.get('/server/link', {
    params: { type, index, sub, ...(raw ? { raw: 1 } : {}) },
  }),
  copyServerToGroup: (ref, groupId, clone = true) => http.post('/server/copy-to-group', { ref, groupId, clone }),
  batchCopyServersToGroup: (refs, groupId, clone = true) => http.post('/servers/batch-copy-to-group', { refs, groupId, clone }),
  batchMoveServersToGroup: (refs, sourceGroupId, targetGroupId) => http.post('/servers/batch-move-to-group', { refs, sourceGroupId, targetGroupId }),

  // 订阅
  getSubscriptions: (view = 'summary') => http.get('/subscriptions', { params: { view } }),
  getSubscriptionServers: (index, page, pageSize) => http.get(`/subscriptions/${index}/servers`, { params: { page, pageSize } }),
  addSubscription: (data) => http.post('/subscriptions', data),
  updateSubscription: (index, data) => http.put(`/subscriptions/${index}`, data),
  deleteSubscription: (index) => http.delete(`/subscriptions/${index}`),
  refreshSubscription: (index) => http.post(`/subscriptions/${index}/update`),

  // 分组
  getGroups: (view = 'summary') => http.get('/groups', { params: { view } }),
  // ref 可传分组 ID 或列表下标，ID 不受分组增删导致的下标漂移影响
  getGroupServers: (ref, page, pageSize, params = {}) =>
    http.get(`/groups/${encodeURIComponent(ref)}/servers`, { params: { page, pageSize, ...params } }),
  createGroup: (name) => http.post('/groups', { name }),
  updateGroup: (index, name) => http.put(`/groups/${index}`, { name }),
  deleteGroup: (index) => http.delete(`/groups/${index}`),
  addServerToGroup: (index, ref) => http.post(`/groups/${index}/servers`, ref),
  removeServerFromGroup: (index, ref) => http.delete(`/groups/${index}/servers`, { data: ref }),
  removeServersFromGroup: (index, refs) => http.post(`/groups/${index}/servers/batch-remove`, { refs }),
  // 分组测速跑完后，按绑定该分组的出站策略决定是否切换活跃节点
  applyGroupSwitch: (index, mode) => http.post(`/groups/${index}/apply-switch`, null, { params: { mode } }),

  // 节点 IP 类型检测
  detectServerIPType: (refs) => http.post('/servers/detect-ip-type', { refs }),

  // 节点 IP 评分：后台异步任务，提交后轮询 status
  scoreServerIP: (refs) => http.post('/servers/ip-score', { refs }),
  getIPScoreStatus: () => http.get('/servers/ip-score/status'),
  cancelIPScore: () => http.post('/servers/ip-score/cancel'),

  // 出站
  getOutbounds: () => http.get('/outbounds'),
  createOutbound: (data) => http.post('/outbounds', data),
  updateOutbound: (name, data) => http.put(`/outbounds/${name}`, data),
  renameOutbound: (name, newName) => http.put(`/outbounds/${encodeURIComponent(name)}/rename`, { name: newName }),
  deleteOutbound: (name) => http.delete(`/outbounds/${name}`),
  // target 之外的 extra 承载出站顶层字段（probeInterval / probeMode），后端按需读取
  connectOutbound: (name, target, extra = {}) => http.post(`/outbounds/${name}/connect`, { ...target, ...extra }),
  setOutboundActiveNode: (name, ref) => http.put(`/outbounds/${name}/active-node`, { ref }),
  disconnectOutbound: (name) => http.post(`/outbounds/${name}/disconnect`),
  downloadCfGoodNetCA: () => window.open('/api/cfgoodnet/ca/download', '_blank'),
  importCfGoodNetCA: () => http.post('/cfgoodnet/ca/import'),

  // 设置
  getSetting: () => http.get('/setting'),
  setSetting: (data) => http.put('/setting', data),

  // 内核状态
  getKernelStatus: () => http.get('/kernels'),
  downloadKernel: (kernel, onProgress) => {
    return new Promise((resolve, reject) => {
      const es = new EventSource(`/api/kernels/${kernel}/download`)
      es.onmessage = (e) => {
        try {
          const data = JSON.parse(e.data)
          onProgress?.(data)
          if (data.status === 'done') { es.close(); resolve(data) }
          else if (data.status === 'error') { es.close(); reject(new Error(data.message)) }
        } catch {}
      }
      es.onerror = (err) => {
        // 只有在非正常关闭时才报错（done/error 后 es 已 close，onerror 会触发一次，忽略）
        if (es.readyState === EventSource.CLOSED) return
        es.close()
        reject(new Error('连接中断'))
      }
    })
  },
  checkKernelUpdate: (kernel) => http.get(`/kernels/${kernel}/check-update`),

  // 数据文件下载
  downloadData: (type, onProgress) => {
    return new Promise((resolve, reject) => {
      const es = new EventSource(`/api/data/${type}/download`)
      es.onmessage = (e) => {
        try {
          const data = JSON.parse(e.data)
          onProgress?.(data)
          if (data.status === 'done') { es.close(); resolve(data) }
          else if (data.status === 'error') { es.close(); reject(new Error(data.message)) }
        } catch {}
      }
      es.onerror = (err) => {
        if (es.readyState === EventSource.CLOSED) return
        es.close()
        reject(new Error('连接中断'))
      }
    })
  },
  // 批量测速：后端为 SSE 流式接口（POST），逐节点推送 running/done 进度。
  // EventSource 只支持 GET，这里用 fetch + ReadableStream 手动解析 SSE。
  // onEvent(event, payload) 会在每个事件到达时回调。
  pingNodes: async (refs, mode = 'fast', { signal, onEvent, enableTransProxyFallback = false } = {}) => {
    const resp = await fetch('/api/ping', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ refs, mode, enableTransProxyFallback }),
      signal,
    })
    if (!resp.ok) {
      if (resp.status === 401) unauthorizedHandler?.({ response: resp })
      throw new Error(`ping 失败: ${resp.status}`)
    }
    const reader = resp.body.getReader()
    const decoder = new TextDecoder()
    let buf = ''
    for (;;) {
      const { done, value } = await reader.read()
      if (done) break
      buf += decoder.decode(value, { stream: true })
      // SSE 事件以空行分隔
      let sep
      while ((sep = buf.indexOf('\n\n')) !== -1) {
        const chunk = buf.slice(0, sep)
        buf = buf.slice(sep + 2)
        let evt = 'message'
        let data = ''
        for (const line of chunk.split('\n')) {
          if (line.startsWith('event:')) evt = line.slice(6).trim()
          else if (line.startsWith('data:')) data += line.slice(5).trim()
        }
        if (!data) continue
        try {
          onEvent?.(evt, JSON.parse(data))
        } catch {}
      }
    }
  },
  pingGroup: (index, mode = 'fast', enableTransProxyFallback = false) => http.post(`/groups/${index}/ping`, null, {
    params: { mode, enableTransProxyFallback },
  }),

  // 日志
  getLogs: (params = {}) => http.get('/logs', { params }),
  getLogsStream: (onMessage, params = {}) => {
    return new Promise((resolve, reject) => {
      const query = new URLSearchParams(params).toString()
      const es = new EventSource(`/api/logs/stream${query ? `?${query}` : ''}`)
      es.onmessage = (e) => {
        try {
          const data = JSON.parse(e.data)
          onMessage?.(data)
        } catch {}
      }
      es.onerror = (err) => {
        if (es.readyState === EventSource.CLOSED) return
        es.close()
        reject(new Error('日志连接中断'))
      }
    })
  },

  // 路由规则
  getRoutingRules: () => http.get('/routing-rules'),
  setRoutingRules: (rules) => http.put('/routing-rules', rules),

  // 自定义入站
  getCustomInbounds: () => http.get('/custom-inbounds'),
  setCustomInbounds: (inbounds) => http.put('/custom-inbounds', inbounds),
  getBuiltinProxyStatuses: () => http.get('/builtin-proxies/status'),
  restartBuiltinProxy: (id) => http.post(`/builtin-proxies/${id}/restart`),

  // rule-set 下载
  downloadRuleSets: (mirror, onProgress) => {
    return new Promise((resolve, reject) => {
      const url = mirror
        ? `/api/rule-sets/download?mirror=${encodeURIComponent(mirror)}`
        : '/api/rule-sets/download'
      const es = new EventSource(url)
      es.onmessage = (e) => {
        try {
          const data = JSON.parse(e.data)
          onProgress?.(data)
          if (data.status === 'done') { es.close(); resolve(data) }
          else if (data.status === 'error') { es.close(); reject(new Error(data.message)) }
        } catch {}
      }
      es.onerror = () => {
        if (es.readyState === EventSource.CLOSED) return
        es.close()
        reject(new Error('连接中断'))
      }
    })
  },
  getRuleSetInfos: () => http.get('/rule-sets/info'),
}
