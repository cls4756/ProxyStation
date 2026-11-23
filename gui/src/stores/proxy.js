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
  const groupLoads = new Map()
  const nodeVersions = new Map()
  let subscriptionsLoaded = false
  let subscriptionSummaryLoad = null
  let subscriptionsGeneration = 0
  let nodeDetailsGeneration = 0
  let groupSummaryGeneration = 0
  let nodeUpdateVersion = 0
  let detailsCacheVersion = 0
  let statusRequestVersion = 0
  let statusCommittedVersion = 0
  let outboundRequestVersion = 0
  let outboundCommittedVersion = 0

  // 实时获取所有数据
  async function fetchStatus() {
    const version = ++statusRequestVersion
    const { data } = await api.getStatus()
    if (version < statusCommittedVersion) return
    statusCommittedVersion = version
    running.value = data.running
  }

  async function fetchServers() {
    const { data } = await api.getServers('list')
    servers.value = data.servers || []
  }

  async function fetchSubscriptions(includeServers = false, force = false) {
    if (!includeServers && force) {
      invalidateSubscriptions()
    }
    if (!includeServers && subscriptionsLoaded && !force) return subscriptions.value
    if (!includeServers && subscriptionSummaryLoad && !force) return subscriptionSummaryLoad
    const generation = subscriptionsGeneration
    const load = api.getSubscriptions(includeServers ? 'list' : 'summary').then(({ data }) => {
      if (generation !== subscriptionsGeneration) return subscriptions.value
      const incoming = data.subscriptions || []
      if (includeServers) {
        const cacheVersion = ++detailsCacheVersion
        subscriptions.value = incoming.map(sub => ({ ...sub, serversLoaded: true, cacheVersion }))
        subscriptionsLoaded = true
        return subscriptions.value
      }
      const previousById = new Map(subscriptions.value.map(sub => [sub.id, sub]))
      subscriptions.value = incoming.map(sub => {
        const current = previousById.get(sub.id)
        const sameSubscription = current?.id === sub.id
        const sameVersion = sameSubscription &&
          Number(current.serverCount ?? current.servers?.length ?? 0) === Number(sub.serverCount || 0) &&
          String(current.updatedAt || '') === String(sub.updatedAt || '')
        return sameVersion && Array.isArray(current.servers)
          ? {
              ...sub, servers: current.servers, serversLoaded: !!current.serversLoaded,
              loadedPages: current.loadedPages || {}, cacheVersion: current.cacheVersion,
            }
          : { ...sub, serversLoaded: false, cacheVersion: ++detailsCacheVersion }
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
    subscriptionSummaryLoad = null
    subscriptionLoads.clear()
    nodeDetailsGeneration += 1
    nodeLoads.clear()
    groupLoads.clear()
    nodeVersions.clear()
  }

  function invalidateNodeDetails() {
    nodeDetailsGeneration += 1
    subscriptionLoads.clear()
    nodeLoads.clear()
    groupLoads.clear()
    nodeVersions.clear()
    const cacheVersion = ++detailsCacheVersion
    servers.value = []
    subscriptions.value = subscriptions.value.map(sub => ({
      ...sub,
      servers: undefined,
      serversLoaded: false,
      loadedPages: {},
      cacheVersion,
    }))
    groups.value = groups.value.map(group => ({
      ...group,
      servers: undefined,
      serversLoaded: false,
      loadedPages: {},
      cacheVersion,
    }))
  }

  async function fetchSubscriptionServers(index, force = false, page = 1, pageSize = 100) {
    const sub = subscriptions.value[index]
    if (!sub) return []
    const pageKey = `${page}:${pageSize}`
    if (!force && sub.loadedPages?.[pageKey] && Array.isArray(sub.servers)) {
      if (pageSize <= 0) return sub.servers.filter(Boolean)
      return sub.servers.slice((page - 1) * pageSize, page * pageSize)
    }
    const loadKey = `${sub.id}:${index}:${pageKey}`
    if (force) {
      for (const key of subscriptionLoads.keys()) {
        if (key.startsWith(`${sub.id}:${index}:`)) subscriptionLoads.delete(key)
      }
      subscriptions.value[index] = { ...sub, loadedPages: {} }
    } else if (subscriptionLoads.has(loadKey)) {
      return subscriptionLoads.get(loadKey)
    }
    const generation = nodeDetailsGeneration
    const summaryGeneration = subscriptionsGeneration
    const nodeVersion = ++nodeUpdateVersion
    const request = api.getSubscriptionServers(index, page, pageSize).then(({ data }) => {
      const current = subscriptions.value[index]
      if (generation !== nodeDetailsGeneration || summaryGeneration !== subscriptionsGeneration ||
          current?.id !== sub.id || subscriptionLoads.get(loadKey) !== request) return []
      if (current) {
        const total = Number(data.serverCount || 0)
        const offset = Number(data.offset || 0)
        const sameSize = Array.isArray(current.servers) && current.servers.length === total
        const servers = sameSize
          ? [...current.servers]
          : new Array(total)
        for (let indexOffset = 0; indexOffset < (data.servers || []).length; indexOffset += 1) {
          const nodeIndex = offset + indexOffset
          const key = `sub_server:${nodeIndex}:${index}`
          if ((nodeVersions.get(key) || 0) > nodeVersion) {
            servers[nodeIndex] = current.servers?.[nodeIndex]
            continue
          }
          servers[nodeIndex] = data.servers[indexOffset]
          nodeVersions.set(key, nodeVersion)
        }
        subscriptions.value[index] = {
          ...current,
          servers,
          serversLoaded: true,
          loadedPages: { ...(sameSize ? current.loadedPages : {}), [pageKey]: true },
          serverCount: total,
          updatedAt: data.updatedAt || current.updatedAt,
        }
      }
      return data.servers || []
    }).finally(() => {
      if (subscriptionLoads.get(loadKey) === request) subscriptionLoads.delete(loadKey)
    })
    subscriptionLoads.set(loadKey, request)
    return request
  }

  function patchNodes(items, version = ++nodeUpdateVersion) {
    let manual = null
    const subscriptionLists = new Map()
    const accepted = []
    for (const item of items || []) {
      const refObj = item?.ref
      const patch = item?.server || item?.patch
      if (!refObj || !patch) continue
      const key = `${refObj.type}:${refObj.index}:${refObj.type === 'server' ? 0 : (refObj.sub || 0)}`
      if ((nodeVersions.get(key) || 0) > version) continue
      nodeVersions.set(key, version)
      accepted.push(item)
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
    return accepted
  }

  function patchNode(refObj, patch) {
    patchNodes([{ ref: refObj, patch }])
  }

  async function fetchNodes(refs, force = false) {
    const unique = []
    const seen = new Set()
    for (const refObj of refs || []) {
      const normalized = { type: refObj.type, index: refObj.index, sub: refObj.type === 'server' ? 0 : (refObj.sub || 0) }
      const key = `${normalized.type}:${normalized.index}:${normalized.sub}`
      if (!seen.has(key)) {
        seen.add(key)
        unique.push(normalized)
      }
    }
    if (!unique.length) return []
    const key = unique.map(refObj => `${refObj.type}:${refObj.index}:${refObj.sub}`).sort().join('|')
    if (!force && nodeLoads.has(key)) return nodeLoads.get(key)
    const generation = nodeDetailsGeneration
    const version = ++nodeUpdateVersion
    const request = api.queryServers(unique).then(({ data }) => {
      if (generation !== nodeDetailsGeneration || nodeLoads.get(key) !== request) return []
      return patchNodes(data.results || [], version)
    }).finally(() => {
      if (nodeLoads.get(key) === request) nodeLoads.delete(key)
    })
    nodeLoads.set(key, request)
    return request
  }

  async function fetchGroups() {
    const generation = ++groupSummaryGeneration
    groupLoads.clear()
    const { data } = await api.getGroups('summary')
    if (generation !== groupSummaryGeneration) return
    const cacheVersion = ++detailsCacheVersion
    groups.value = (data.groups || []).map(group => ({ ...group, serversLoaded: false, cacheVersion }))
  }

  function invalidateGroupLoads(groupId) {
    for (const key of groupLoads.keys()) {
      if (key.startsWith(`${groupId}:`)) groupLoads.delete(key)
    }
  }

  async function fetchGroupServers(index, force = false, page = 1, pageSize = 100) {
    const group = groups.value[index]
    if (!group) return []
    const pageKey = `${page}:${pageSize}`
    if (!force && group.loadedPages?.[pageKey] && Array.isArray(group.servers)) {
      if (pageSize <= 0) return group.servers.filter(Boolean)
      return group.servers.slice((page - 1) * pageSize, page * pageSize)
    }
    const loadKey = `${group.id}:${pageKey}`
    if (force) {
      invalidateGroupLoads(group.id)
      groups.value[index] = { ...group, loadedPages: {} }
    } else if (groupLoads.has(loadKey)) {
      return groupLoads.get(loadKey)
    }
    const generation = nodeDetailsGeneration
    const nodeVersion = ++nodeUpdateVersion
    const request = api.getGroupServers(group.id, page, pageSize).then(({ data }) => {
      if (generation !== nodeDetailsGeneration || groupLoads.get(loadKey) !== request) return []
      const currentIndex = groups.value.findIndex(item => item.id === group.id)
      if (currentIndex < 0) return []
      const current = groups.value[currentIndex]
      const total = Number(data.serverCount || 0)
      const offset = Number(data.offset || 0)
      const sameSize = Array.isArray(current.servers) && current.servers.length === total
      const refs = sameSize
        ? [...current.servers]
        : new Array(total)
      for (let i = 0; i < (data.refs || []).length; i += 1) {
        refs[offset + i] = data.refs[i]
      }
      const next = [...groups.value]
      next[currentIndex] = {
        ...current,
        servers: refs,
        serversLoaded: true,
        loadedPages: { ...(sameSize ? current.loadedPages : {}), [pageKey]: true },
        serverCount: total,
      }
      groups.value = next
      return patchNodes(data.results || [], nodeVersion)
    }).finally(() => {
      if (groupLoads.get(loadKey) === request) groupLoads.delete(loadKey)
    })
    groupLoads.set(loadKey, request)
    return request
  }

  async function fetchOutbounds() {
    const version = ++outboundRequestVersion
    const generation = nodeDetailsGeneration
    const { data } = await api.getOutbounds()
    if (generation !== nodeDetailsGeneration || version < outboundCommittedVersion) return
    outboundCommittedVersion = version
    outbounds.value = data.outbounds || []
  }

  function patchGroup(group) {
    if (!group?.id) return
    const index = groups.value.findIndex(item => item.id === group.id)
    if (index < 0) return
    invalidateGroupLoads(group.id)
    const next = [...groups.value]
    next[index] = {
      ...group,
      serverCount: Number(group.serverCount ?? group.servers?.length ?? 0),
      serversLoaded: Array.isArray(group.servers),
      loadedPages: Array.isArray(group.servers) ? {} : (group.loadedPages || {}),
      cacheVersion: ++detailsCacheVersion,
    }
    groups.value = next
  }

  function patchGroupCount(groupId, serverCount) {
    const index = groups.value.findIndex(item => item.id === groupId)
    if (index < 0) return
    invalidateGroupLoads(groupId)
    const next = [...groups.value]
    next[index] = {
      ...next[index], serverCount: Number(serverCount || 0),
      servers: undefined, serversLoaded: false, loadedPages: {},
      cacheVersion: ++detailsCacheVersion,
    }
    groups.value = next
  }

  async function fetchCustomInbounds() {
    const { data } = await api.getCustomInbounds()
    customInbounds.value = data.inbounds || []
  }

  async function fetchAll({ includeSubscriptions = true, includeCustomInbounds = true } = {}) {
    const requests = [
      fetchStatus(),
      fetchGroups(),
      fetchOutbounds(),
    ]
    if (includeSubscriptions) requests.push(fetchSubscriptions(false))
    if (includeCustomInbounds) requests.push(fetchCustomInbounds())
    await Promise.all(requests)
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
    invalidateNodeDetails,
    fetchSubscriptionServers,
    fetchNodes,
    patchNode,
    patchNodes,
    fetchGroups,
    fetchGroupServers,
    patchGroup,
    patchGroupCount,
    fetchOutbounds,
    fetchCustomInbounds,
    fetchAll, 
    toggleProxy 
  }
})
