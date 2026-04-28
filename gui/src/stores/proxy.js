import { defineStore } from 'pinia'
import { ref } from 'vue'
import { api } from '../api'

export const useProxyStore = defineStore('proxy', () => {
  const running = ref(false)
  const servers = ref([])
  const subscriptions = ref([])
  const groups = ref([])
  const outbounds = ref([])

  // 实时获取所有数据
  async function fetchStatus() {
    const { data } = await api.getStatus()
    running.value = data.running
  }

  async function fetchServers() {
    const { data } = await api.getServers()
    servers.value = data.servers || []
  }

  async function fetchSubscriptions() {
    const { data } = await api.getSubscriptions()
    subscriptions.value = data.subscriptions || []
  }

  async function fetchGroups() {
    const { data } = await api.getGroups()
    groups.value = data.groups || []
  }

  async function fetchOutbounds() {
    const { data } = await api.getOutbounds()
    outbounds.value = data.outbounds || []
  }

  async function fetchAll() {
    await Promise.all([
      fetchStatus(),
      fetchServers(),
      fetchSubscriptions(),
      fetchGroups(),
      fetchOutbounds(),
    ])
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
    fetchStatus, 
    fetchServers,
    fetchSubscriptions,
    fetchGroups,
    fetchOutbounds,
    fetchAll, 
    toggleProxy 
  }
})
