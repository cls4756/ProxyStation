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
  getStatus: () => http.get('/status'),
  startProxy: () => http.post('/start'),
  stopProxy: () => http.post('/stop'),

  // 节点
  getServers: () => http.get('/servers'),
  searchServers: (q) => http.get('/servers/search', { params: { q } }),
  import: (data) => http.post('/import', data),
  importServers: (links, groupId) => http.post('/servers', { links, groupId }),
  deleteServers: (indexes) => http.delete('/servers', { data: { indexes } }),
  editServer: (index, data) => http.put(`/servers/${index}`, data),
  getServerLink: (type, index, sub = 0) => http.get('/server/link', { params: { type, index, sub } }),
  copyServerToGroup: (ref, groupId) => http.post('/server/copy-to-group', { ref, groupId }),

  // 订阅
  getSubscriptions: () => http.get('/subscriptions'),
  addSubscription: (data) => http.post('/subscriptions', data),
  updateSubscription: (index, data) => http.put(`/subscriptions/${index}`, data),
  deleteSubscription: (index) => http.delete(`/subscriptions/${index}`),
  refreshSubscription: (index) => http.post(`/subscriptions/${index}/update`),

  // 分组
  getGroups: () => http.get('/groups'),
  createGroup: (name) => http.post('/groups', { name }),
  updateGroup: (index, name) => http.put(`/groups/${index}`, { name }),
  deleteGroup: (index) => http.delete(`/groups/${index}`),
  addServerToGroup: (index, ref) => http.post(`/groups/${index}/servers`, ref),
  removeServerFromGroup: (index, ref) => http.delete(`/groups/${index}/servers`, { data: ref }),

  // 出站
  getOutbounds: () => http.get('/outbounds'),
  createOutbound: (data) => http.post('/outbounds', data),
  updateOutbound: (name, data) => http.put(`/outbounds/${name}`, data),
  deleteOutbound: (name) => http.delete(`/outbounds/${name}`),
  connectOutbound: (name, target) => http.post(`/outbounds/${name}/connect`, target),
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
  pingNodes: (refs, mode = 'fast', config = {}) => http.post('/ping', { refs, mode }, config),
  pingGroup: (index, mode = 'fast') => http.post(`/groups/${index}/ping`, null, { params: { mode } }),

  // 日志
  getLogs: () => http.get('/logs'),
  getLogsStream: (onMessage) => {
    return new Promise((resolve, reject) => {
      const es = new EventSource('/api/logs/stream')
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
