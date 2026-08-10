import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api'

export const useProxyStore = defineStore('proxy', () => {
  const running = ref(false)
  const servers = ref([])
  const subscriptions = ref([])
  const groups = ref([])
  const outbounds = ref([])
  const customInbounds = ref([])
  const subscriptionLoads = new Map()
  const nodeLoads = new Map()
  let subscriptionsLoaded = false
  let subscriptionSummaryLoad = null
  let subscriptionsGeneration = 0

  // 实时获取所有数据
  async function fetchStatus() {
    const { data } = await api.getStatus()
    running.value = data.running
  }

  async function fetchServers() {
    const { data } = await api.getServers('list')
    servers.value = data.servers || []
  }

  async function fetchSubscriptions(includeServers = false, force = false) {
    if (!includeServers && force) {
      subscriptionsLoaded = false
      subscriptionsGeneration += 1
    }
    if (!includeServers && subscriptionsLoaded && !force) return subscriptions.value
    if (!includeServers && subscriptionSummaryLoad && !force) return subscriptionSummaryLoad
    const generation = subscriptionsGeneration
    const load = api.getSubscriptions(includeServers ? 'list' : 'summary').then(({ data }) => {
      if (!includeServers && generation !== subscriptionsGeneration) return subscriptions.value
      const incoming = data.subscriptions || []
      if (includeServers) {
        subscriptions.value = incoming.map(sub => ({ ...sub, serversLoaded: true }))
        subscriptionsLoaded = true
        return subscriptions.value
      }
      subscriptions.value = incoming.map((sub, index) => {
        const current = subscriptions.value[index]
        const sameSubscription = current?.id === sub.id
        const sameVersion = sameSubscription &&
          Number(current.serverCount ?? current.servers?.length ?? 0) === Number(sub.serverCount || 0) &&
          String(current.updatedAt || '') === String(sub.updatedAt || '')
        return sameVersion && Array.isArray(current.servers)
          ? { ...sub, servers: current.servers, serversLoaded: !!current.serversLoaded, loadedPages: current.loadedPages || {} }
          : { ...sub, serversLoaded: false }
      })
      subscriptionsLoaded = true
      return subscriptions.value
    })
    if (includeServers) return load
    const request = load.finally(() => {
      if (subscriptionSummaryLoad === request) subscriptionSummaryLoad = null
    })
    subscriptionSummaryLoad = request
    return subscriptionSummaryLoad
  }

  function invalidateSubscriptions() {
    subscriptionsLoaded = false
    subscriptionsGeneration += 1
  }

  async function fetchSubscriptionServers(index, force = false, page = 1, pageSize = 100) {
    const sub = subscriptions.value[index]
    if (!sub) return []
    const pageKey = `${page}:${pageSize}`
    if (!force && sub.loadedPages?.[pageKey] && Array.isArray(sub.servers)) {
      return sub.servers.slice((page - 1) * pageSize, page * pageSize)
    }
    const loadKey = `${index}:${pageKey}`
    if (subscriptionLoads.has(loadKey)) return subscriptionLoads.get(loadKey)
    const request = api.getSubscriptionServers(index, page, pageSize).then(({ data }) => {
      const current = subscriptions.value[index]
      if (current) {
        const total = Number(data.serverCount || 0)
        const offset = Number(data.offset || 0)
        const servers = Array.isArray(current.servers) && current.servers.length === total
          ? [...current.servers]
          : new Array(total)
        for (let i = 0; i < (data.servers || []).length; i += 1) {
          servers[offset + i] = data.servers[i]
        }
        subscriptions.value[index] = {
          ...current,
          servers,
          serversLoaded: true,
          loadedPages: { ...(current.loadedPages || {}), [pageKey]: true },
          serverCount: total,
          updatedAt: data.updatedAt || current.updatedAt,
        }
      }
      return data.servers || []
    }).finally(() => subscriptionLoads.delete(loadKey))
    subscriptionLoads.set(loadKey, request)
    return request
  }

  function patchNodes(items) {
    let manual = null
    const subscriptionLists = new Map()
    for (const item of items || []) {
      const refObj = item?.ref
      const patch = item?.server || item?.patch
      if (!refObj || !patch) continue
      if (refObj.type === 'server') {
        if (!manual) manual = [...servers.value]
        manual[refObj.index] = { ...(manual[refObj.index] || {}), ...patch }
        continue
      }
      if (refObj.type !== 'sub_server') continue
      const subIndex = refObj.sub || 0
      const sub = subscriptions.value[subIndex]
      if (!sub) continue
      let list = subscriptionLists.get(subIndex)
      if (!list) {
        list = Array.isArray(sub.servers)
          ? [...sub.servers]
          : new Array(Number(sub.serverCount || 0))
        subscriptionLists.set(subIndex, list)
      }
      list[refObj.index] = { ...(list[refObj.index] || {}), ...patch }
    }
    if (manual) servers.value = manual
    if (subscriptionLists.size) {
      const next = [...subscriptions.value]
      for (const [subIndex, list] of subscriptionLists) {
        next[subIndex] = { ...next[subIndex], servers: list }
      }
      subscriptions.value = next
    }
  }

  function patchNode(refObj, patch) {
    patchNodes([{ ref: refObj, patch }])
  }

  async function fetchNodes(refs) {
    const unique = []
    const seen = new Set()
    for (const refObj of refs || []) {
      const normalized = { type: refObj.type, index: refObj.index, sub: refObj.sub || 0 }
      const key = `${normalized.type}:${normalized.index}:${normalized.sub}`
      if (!seen.has(key)) {
        seen.add(key)
        unique.push(normalized)
      }
    }
    if (!unique.length) return []
    const key = unique.map(refObj => `${refObj.type}:${refObj.index}:${refObj.sub}`).sort().join('|')
    if (nodeLoads.has(key)) return nodeLoads.get(key)
    const request = api.queryServers(unique).then(({ data }) => {
      patchNodes(data.results || [])
      return data.results || []
    }).finally(() => nodeLoads.delete(key))
    nodeLoads.set(key, request)
    return request
  }

  async function fetchGroups() {
    const { data } = await api.getGroups()
    groups.value = data.groups || []
  }

  async function fetchOutbounds() {
    const { data } = await api.getOutbounds()
    outbounds.value = data.outbounds || []
  }

  function patchGroup(group) {
    if (!group?.id) return
    const index = groups.value.findIndex(item => item.id === group.id)
    if (index < 0) return
    const next = [...groups.value]
    next[index] = group
    groups.value = next
  }

  async function fetchCustomInbounds() {
    const { data } = await api.getCustomInbounds()
    customInbounds.value = data.inbounds || []
  }

  function applyBootstrap(data = {}) {
    running.value = !!data.running
    servers.value = []
    const incoming = data.subscriptions || []
    subscriptions.value = incoming.map(sub => ({ ...sub, serversLoaded: false }))
    subscriptionsLoaded = true
    subscriptionSummaryLoad = null
    groups.value = data.groups || []
    outbounds.value = data.outbounds || []
    customInbounds.value = data.customInbounds || []
    patchNodes(data.nodes || [])
  }

  async function fetchAll() {
    await Promise.all([
      fetchStatus(),
      fetchSubscriptions(false),
      fetchGroups(),
      fetchOutbounds(),
      fetchCustomInbounds(),
    ])
    const refs = []
    for (const outbound of outbounds.value) {
      const target = outbound?.target
      if (target?.nodeRef) refs.push(target.nodeRef)
      if (target?.activeNodeRef) refs.push(target.activeNodeRef)
    }
    await fetchNodes(refs)
  }

  async function toggleProxy() {
    if (running.value) {
      await api.stopProxy()
    } else {
      try {
        const { data } = await api.startProxy()
        if (data.error) throw new Error(data.error)
      } catch (e) {
        // 启动失败时，立即刷新状态确保 UI 同步
        await fetchStatus()
        throw e
      }
    }
    await fetchStatus()
  }

  return { 
    running, 
    servers, 
    subscriptions, 
    groups, 
    outbounds, 
    customInbounds,
    fetchStatus, 
    fetchServers,
    fetchSubscriptions,
    invalidateSubscriptions,
    fetchSubscriptionServers,
    fetchNodes,
    patchNode,
    patchNodes,
    fetchGroups,
    patchGroup,
    fetchOutbounds,
    fetchCustomInbounds,
    applyBootstrap,
    fetchAll, 
    toggleProxy 
  }
})
