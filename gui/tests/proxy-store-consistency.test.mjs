import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { afterEach } from 'node:test'
import { createPinia, defineStore } from 'pinia'
import { ref } from 'vue'

const source = readFileSync(new URL('../src/stores/proxy.js', import.meta.url), 'utf8')
const createStore = new Function('defineStore', 'ref', 'api', `
${source.replace(/^import .*$/gm, '').replace('export const useProxyStore', 'const useProxyStore')}
return useProxyStore
`)
const stores = []
afterEach(() => {
  for (const store of stores.splice(0)) store.$dispose()
})

function setupStore(api) {
  const store = createStore(defineStore, ref, api)(createPinia())
  stores.push(store)
  return store
}

function deferred() {
  let resolve, reject
  const promise = new Promise((finish, fail) => { resolve = finish; reject = fail })
  return { promise, resolve, reject }
}

function groupPage(refs, total = refs.length) {
  return { data: { refs, serverCount: total, offset: 0, results: refs.map(refObj => ({
    ref: refObj, server: { name: `node-${refObj.index}` },
  })) } }
}

test('分组数量相同的写操作也失效分页缓存，只补载当前页', async () => {
  const calls = []
  const store = setupStore({
    getGroupServers: async (...args) => {
      calls.push(args)
      return { data: {
        refs: [{ type: 'server', index: 200 }], serverCount: 10000, offset: 200,
        results: [{ ref: { type: 'server', index: 200 }, server: { name: '新节点' } }],
      } }
    },
  })
  store.groups = [{ id: 'group', serverCount: 10000, servers: [{ type: 'server', index: 1 }],
    serversLoaded: true, loadedPages: { '3:100': true } }]

  store.patchGroupCount('group', 10000)
  assert.equal(store.groups[0].serverCount, 10000)
  assert.equal(store.groups[0].serversLoaded, false)
  assert.deepEqual(store.groups[0].loadedPages, {})
  await store.fetchGroupServers(0, false, 3, 100)

  assert.deepEqual(calls, [['group', 3, 100]])
  assert.equal(store.groups[0].servers[200].index, 200)
  assert.equal(store.servers[200].name, '新节点')
  assert.equal(store.groups[0].serverCount, 10000)
})

test('分组失效后的旧页响应不能覆盖新总数或清除新请求', async () => {
  const requests = []
  const store = setupStore({
    getGroupServers: () => {
      const request = deferred()
      requests.push(request)
      return request.promise
    },
  })
  store.groups = [{ id: 'group', serverCount: 1 }]
  const older = store.fetchGroupServers(0)
  store.patchGroupCount('group', 2)
  const newer = store.fetchGroupServers(0)
  requests[0].resolve(groupPage([{ type: 'server', index: 0 }], 1))
  await older
  assert.equal(store.groups[0].serverCount, 2)
  const coalesced = store.fetchGroupServers(0)
  assert.equal(requests.length, 2)
  requests[1].resolve(groupPage([{ type: 'server', index: 1 }, { type: 'server', index: 2 }]))
  await Promise.all([newer, coalesced])
  assert.deepEqual(store.groups[0].servers.map(refObj => refObj.index), [1, 2])
})

test('订阅删除后下标复用时按稳定 ID 发请求，旧响应不能串入新订阅', async () => {
  const requests = []
  const store = setupStore({
    getSubscriptionServers: () => {
      const request = deferred()
      requests.push(request)
      return request.promise
    },
  })
  store.subscriptions = [{ id: 'old', serverCount: 1 }, { id: 'new', serverCount: 1 }]
  const older = store.fetchSubscriptionServers(0)
  store.subscriptions = [store.subscriptions[1]]
  const newer = store.fetchSubscriptionServers(0)
  assert.equal(requests.length, 2)
  requests[0].resolve({ data: { serverCount: 1, servers: [{ name: '旧订阅节点' }] } })
  await older
  assert.equal(store.subscriptions[0].servers, undefined)
  requests[1].resolve({ data: { serverCount: 1, servers: [{ name: '新订阅节点' }] } })
  await newer
  assert.equal(store.subscriptions[0].servers[0].name, '新订阅节点')
})

test('订阅摘要失效后不复用旧请求，旧 finally 不清除新请求', async () => {
  const requests = []
  const store = setupStore({
    getSubscriptions: () => {
      const request = deferred()
      requests.push(request)
      return request.promise
    },
  })
  const older = store.fetchSubscriptions()
  store.invalidateSubscriptions()
  const newer = store.fetchSubscriptions()
  requests[0].resolve({ data: { subscriptions: [{ id: 'old', serverCount: 1 }] } })
  await older
  const coalesced = store.fetchSubscriptions()
  assert.equal(requests.length, 2)
  requests[1].resolve({ data: { subscriptions: [{ id: 'new', serverCount: 10 }] } })
  await Promise.all([newer, coalesced])
  assert.equal(store.subscriptions[0].id, 'new')
})

test('订阅失效也不复用携带旧订阅下标的分组请求', async () => {
  const requests = []
  const store = setupStore({
    getGroupServers: () => {
      const request = deferred()
      requests.push(request)
      return request.promise
    },
  })
  store.groups = [{ id: 'group', serverCount: 1 }]
  const older = store.fetchGroupServers(0)
  store.invalidateSubscriptions()
  const newer = store.fetchGroupServers(0)
  assert.equal(requests.length, 2)
  requests[0].resolve(groupPage([{ type: 'server', index: 0 }]))
  await older
  requests[1].resolve(groupPage([{ type: 'server', index: 1 }]))
  await newer
  assert.equal(store.groups[0].servers[0].index, 1)
})

test('详情失效后旧节点查询不能回写，也不能清除新查询', async () => {
  const requests = []
  const refObj = { type: 'server', index: 4 }
  const store = setupStore({
    queryServers: () => {
      const request = deferred()
      requests.push(request)
      return request.promise
    },
  })
  const older = store.fetchNodes([refObj])
  store.invalidateNodeDetails()
  const newer = store.fetchNodes([refObj])
  requests[0].resolve({ data: { results: [{ ref: refObj, server: { name: '旧节点' } }] } })
  await older
  assert.equal(store.servers[4], undefined)
  const coalesced = store.fetchNodes([refObj])
  assert.equal(requests.length, 2)
  requests[1].resolve({ data: { results: [{ ref: refObj, server: { name: '新节点' } }] } })
  await Promise.all([newer, coalesced])
  assert.equal(store.servers[4].name, '新节点')
})

test('不同引用集合的乱序查询按节点版本合并，保留较新字段', async () => {
  const requests = []
  const first = { type: 'server', index: 4 }
  const last = { type: 'server', index: 5 }
  const store = setupStore({
    queryServers: () => {
      const request = deferred()
      requests.push(request)
      return request.promise
    },
  })
  const older = store.fetchNodes([first, last])
  const newer = store.fetchNodes([first], true)
  requests[1].resolve({ data: { results: [{ ref: first, server: { ipScore: 90 } }] } })
  await newer
  requests[0].resolve({ data: { results: [
    { ref: first, server: { ipScore: 20 } }, { ref: last, server: { ipScore: 30 } },
  ] } })
  const accepted = await older
  assert.equal(store.servers[4].ipScore, 90)
  assert.equal(store.servers[5].ipScore, 30)
  assert.equal(accepted.length, 1)
  assert.equal(accepted[0].ref.index, 5)
})

test('SSE 实时字段不被先发后到的节点查询覆盖', async () => {
  const request = deferred()
  const refObj = { type: 'server', index: 4 }
  const store = setupStore({ queryServers: () => request.promise })
  const pending = store.fetchNodes([refObj])
  store.patchNode(refObj, { fastLatency: 30, lastFastProbeTime: 100 })
  request.resolve({ data: { results: [{ ref: refObj, server: { fastLatency: -1, lastFastProbeTime: 10 } }] } })
  await pending
  assert.equal(store.servers[4].fastLatency, 30)
})

test('订阅页旧响应保留已经补载的较新节点元数据', async () => {
  const request = deferred()
  const store = setupStore({ getSubscriptionServers: () => request.promise })
  store.subscriptions = [{ id: 'sub', serverCount: 10 }]
  const pending = store.fetchSubscriptionServers(0)
  store.patchNode({ type: 'sub_server', index: 0 }, { name: '节点', ipScore: 90 })
  request.resolve({ data: { servers: [{ name: '旧名称', ipScore: 20 }], serverCount: 10, offset: 0 } })
  await pending
  assert.equal(store.subscriptions[0].servers[0].name, '节点')
  assert.equal(store.subscriptions[0].servers[0].ipScore, 90)
})

test('分组摘要乱序响应不能覆盖更新后的对象与独立总数', async () => {
  const requests = []
  const store = setupStore({
    getGroups: () => {
      const request = deferred()
      requests.push(request)
      return request.promise
    },
  })
  const older = store.fetchGroups()
  const newer = store.fetchGroups()
  requests[1].resolve({ data: { groups: [{ id: 'new', serverCount: 10000 }] } })
  await newer
  requests[0].resolve({ data: { groups: [{ id: 'old', serverCount: 1 }] } })
  await older
  assert.equal(store.groups[0].id, 'new')
  assert.equal(store.groups[0].serverCount, 10000)
})

test('局部分组引用片段不能替代独立总数', () => {
  const store = setupStore({})
  store.groups = [{ id: 'group', serverCount: 10000 }]
  store.patchGroup({ id: 'group', serverCount: 10000, servers: [{ type: 'server', index: 4 }] })
  assert.equal(store.groups[0].serverCount, 10000)
  assert.equal(store.groups[0].servers.length, 1)
})

for (const descriptor of [
  { method: 'fetchOutbounds', apiMethod: 'getOutbounds', field: 'outbounds', fresh: [{ name: 'new' }], stale: [{ name: 'old' }] },
  { method: 'fetchStatus', apiMethod: 'getStatus', field: 'running', fresh: true, stale: false },
]) {
  test(`${descriptor.field} 的旧响应不能覆盖已完成的新响应`, async () => {
    const requests = []
    const store = setupStore({
      [descriptor.apiMethod]: () => {
        const request = deferred()
        requests.push(request)
        return request.promise
      },
    })
    const older = store[descriptor.method]()
    const newer = store[descriptor.method]()
    requests[1].resolve({ data: { [descriptor.field]: descriptor.fresh } })
    await newer
    requests[0].resolve({ data: { [descriptor.field]: descriptor.stale } })
    await older
    assert.deepEqual(store[descriptor.field], descriptor.fresh)
  })
}
