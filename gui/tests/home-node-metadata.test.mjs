import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { afterEach } from 'node:test'
import { createPinia, defineStore } from 'pinia'
import { compileScript, parse } from 'vue/compiler-sfc'
import { renderToString } from 'vue/server-renderer'
import * as vue from 'vue'

const homeSource = readFileSync(new URL('../src/views/HomeView.vue', import.meta.url), 'utf8')
const { descriptor } = parse(homeSource)
const homeScript = compileScript(descriptor, { id: 'home-node-metadata-test' }).content
const createHome = new Function(
  'ref', 'computed', 'inject', 'watch', 'onMounted', 'onBeforeUnmount', 'nextTick',
  'useProxyStore', 'api', 'ServerRow', 'ServerListHead', 'NodeFilterPanel', 'NodeMenu', 'axios',
  homeScript.replace(/^import .*$/gm, '').replace('export default', 'return'),
)
const storeSource = readFileSync(new URL('../src/stores/proxy.js', import.meta.url), 'utf8')
const createStore = new Function(
  'defineStore', 'ref', 'api',
  `${storeSource.replace(/^import .*$/gm, '').replace('export const useProxyStore', 'const useProxyStore')}
return useProxyStore`,
)

const homeCleanups = []
afterEach(() => {
  for (const cleanup of homeCleanups.splice(0)) cleanup()
})

async function setupHome(api) {
  const pinia = createPinia()
  const useProxyStore = createStore(defineStore, vue.ref, api)
  const store = useProxyStore(pinia)
  const component = createHome(
    vue.ref, vue.computed, vue.inject, vue.watch, vue.onMounted, vue.onBeforeUnmount, vue.nextTick,
    () => store, api, null, null, null, null, {},
  )
  let view
  const app = vue.createSSRApp({
    setup(props, setupContext) {
      view = component.setup(props, setupContext)
      return () => null
    },
  })
  app.use(pinia)
  app.provide('openImport', () => {})
  app.provide('currentOutbound', vue.ref('proxy'))
  await renderToString(app)
  homeCleanups.push(() => {
    clearTimeout(view.noticeTimer)
    view.stopIPScorePoll()
    store.$dispose()
  })
  return { view, store }
}

function setSearchResults(view, refObj, server) {
  view.isSearching.value = true
  view.searchTotal.value = 10000
  view.searchResults.value = [
    { ref: refObj, server, groupNames: ['测试分组'] },
    { ref: { type: 'server', index: 1 }, server: { name: '未选节点', ipScore: 60 } },
  ]
  view.selectedRefs.value = [refObj]
}

for (const refObj of [
  { type: 'server', index: 0 },
  { type: 'sub_server', index: 7000, sub: 2 },
]) {
  test(`筛选节点的 IP 类型完成后同步展示：${refObj.type}`, async () => {
    const queue = [{ ...refObj, sub: refObj.sub || 0 }]
    const calls = []
    const metadata = { name: '选中节点', ipType: 'residential', ipTypeAddr: '1.1.1.1', ipTypeIsp: 'ISP' }
    const { view, store } = await setupHome({
      detectServerIPType: async refs => { calls.push(['detect', refs]) },
      queryServers: async refs => {
        calls.push(['query', refs])
        return { data: { results: [{ ref: refObj, server: metadata }] } }
      },
    })
    store.subscriptions = Array.from({ length: 3 }, (_, index) => ({
      id: `sub-${index}`, serverCount: 10000, serversLoaded: false,
    }))
    setSearchResults(view, refObj, { name: '选中节点', ipType: '' })

    await view.detectIPTypeForRefs(view.selectedRefs.value)

    assert.deepEqual(calls, [['detect', queue], ['query', queue]])
    assert.deepEqual(view.sortedSearchResults.value[0].server, metadata)
    assert.equal(view.singleSelectedServer.value.ipType, 'residential')
    assert.deepEqual([...view.searchResults.value[0].groupNames], ['测试分组'])
    assert.equal(view.searchResults.value[1].server.ipScore, 60)
    assert.equal(view.currentNodeTotal.value, 10000)
    assert.equal(view.detectingIPType.value, false)
    assert.deepEqual(view.ipTypeStates.value, {})
    const cached = refObj.type === 'server'
      ? store.servers[refObj.index]
      : store.subscriptions[refObj.sub].servers[refObj.index]
    assert.equal(cached.ipType, 'residential')
  })

  test(`筛选节点的 IP 评分完成后同步展示：${refObj.type}`, async () => {
    const calls = []
    const { view } = await setupHome({
      getIPScoreStatus: async () => ({
        data: { state: { running: false, total: 1, done: 1, failed: 0, refs: [refObj] } },
      }),
      queryServers: async refs => {
        calls.push(refs)
        return { data: { results: [{
          ref: refObj,
          server: { name: '选中节点', ipScore: 85, ipScoreGrade: 'A', ipScoreAt: 123 },
        }] } }
      },
    })
    setSearchResults(view, refObj, { name: '选中节点', ipScore: null })
    view.ipScoreTask.value = { running: true }
    view.ipScoringRefs.value = { [view.refKey(refObj)]: true }

    await view.pollIPScore()

    assert.deepEqual(calls, [[{ ...refObj, sub: refObj.sub || 0 }]])
    assert.equal(view.sortedSearchResults.value[0].server.ipScore, 85)
    assert.equal(view.singleSelectedServer.value.ipScoreGrade, 'A')
    assert.equal(view.searchResults.value[1].server.ipScore, 60)
    assert.equal(view.currentNodeTotal.value, 10000)
    assert.deepEqual(view.ipScoringRefs.value, {})
    assert.equal(view.ipScoreTask.value.running, false)
  })
}

test('IP 类型请求未完成时保留原列表并显示检测状态', async () => {
  let finishDetection
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    detectServerIPType: () => new Promise(resolve => { finishDetection = resolve }),
    queryServers: async () => ({ data: { results: [{ ref: refObj, server: { ipType: 'idc' } }] } }),
  })
  setSearchResults(view, refObj, { name: '选中节点', ipType: '' })

  const pending = view.detectIPTypeForRefs([refObj])
  assert.equal(view.detectingIPType.value, true)
  assert.equal(view.ipTypeStates.value[view.refKey(refObj)].status, 'running')
  assert.equal(view.sortedSearchResults.value.length, 2)
  assert.equal(view.currentNodeTotal.value, 10000)
  finishDetection()
  await pending
  assert.equal(view.sortedSearchResults.value[0].server.ipType, 'idc')
})

test('重新筛选后旧节点查询不得覆盖新结果', async () => {
  let finishQuery
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    queryServers: () => new Promise(resolve => { finishQuery = resolve }),
    searchServers: async () => ({
      data: { results: [{ ref: refObj, server: { name: '新筛选结果', ipScore: 90 } }], total: 3000 },
    }),
  })
  setSearchResults(view, refObj, { name: '旧筛选结果', ipScore: 10 })
  view.appliedSearchFilters.value = { ...view.emptySearchFilters(), type: 'vless' }

  const pending = view.refreshNodeResults([refObj])
  await view.onSearch(false)
  finishQuery({ data: { results: [{ ref: refObj, server: { name: '过期结果', ipScore: 20 } }] } })
  await pending

  assert.equal(view.sortedSearchResults.value[0].server.name, '新筛选结果')
  assert.equal(view.sortedSearchResults.value[0].server.ipScore, 90)
  assert.equal(view.currentNodeTotal.value, 3000)
  assert.equal(view.searchLoading.value, false)
})

test('空的补载结果不清空筛选页或改变独立总数', async () => {
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    queryServers: async () => ({ data: { results: [] } }),
  })
  setSearchResults(view, refObj, { name: '选中节点', ipScore: 80 })

  await view.refreshNodeResults([refObj])

  assert.equal(view.sortedSearchResults.value.length, 2)
  assert.equal(view.sortedSearchResults.value[0].server.ipScore, 80)
  assert.equal(view.currentNodeTotal.value, 10000)
})

for (const shouldFail of [false, true]) {
  test(`旧筛选请求${shouldFail ? '失败' : '返回'}不覆盖新筛选结果`, async () => {
    const requests = []
    const refObj = { type: 'server', index: 0 }
    const { view } = await setupHome({
      searchServers: () => new Promise((resolve, reject) => { requests.push({ resolve, reject }) }),
    })
    view.appliedSearchFilters.value = { ...view.emptySearchFilters(), type: 'vless' }

    const older = view.onSearch(false)
    assert.equal(view.searchLoading.value, true)
    const newer = view.onSearch(false)
    requests[1].resolve({
      data: { results: [{ ref: refObj, server: { name: '新结果', ipScore: 90 } }], total: 5000 },
    })
    await newer
    if (shouldFail) {
      requests[0].reject(new Error('旧请求失败'))
    } else {
      requests[0].resolve({ data: { results: [], total: 0 } })
    }
    await older

    assert.equal(view.sortedSearchResults.value[0].server.name, '新结果')
    assert.equal(view.currentNodeTotal.value, 5000)
    assert.equal(view.searchLoading.value, false)
  })
}

test('评分仍在运行时不补载节点也不清除运行标记', async () => {
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    getIPScoreStatus: async () => ({ data: { state: { running: true, total: 1, done: 0, failed: 0 } } }),
    queryServers: () => { assert.fail('运行中的任务不应查询节点') },
  })
  setSearchResults(view, refObj, { name: '选中节点', ipScore: null })
  view.ipScoringRefs.value = { [view.refKey(refObj)]: true }

  await view.pollIPScore()

  assert.equal(view.ipScoringRefs.value[view.refKey(refObj)], true)
  assert.equal(view.sortedSearchResults.value.length, 2)
})

test('评分完成后补载失败保留已有结果并清除运行标记', async () => {
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    getIPScoreStatus: async () => ({
      data: { state: { running: false, total: 1, done: 1, failed: 0, refs: [refObj] } },
    }),
    queryServers: async () => { throw new Error('查询失败') },
  })
  setSearchResults(view, refObj, { name: '选中节点', ipScore: 70 })
  view.ipScoringRefs.value = { [view.refKey(refObj)]: true }

  await view.pollIPScore()

  assert.equal(view.sortedSearchResults.value[0].server.ipScore, 70)
  assert.equal(view.currentNodeTotal.value, 10000)
  assert.deepEqual(view.ipScoringRefs.value, {})
})

test('IP 类型改变后重算当前筛选页与独立总数，移除不再匹配的选中节点', async () => {
  const calls = []
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    detectServerIPType: async () => {},
    queryServers: async () => ({ data: { results: [{ ref: refObj, server: { ipType: 'residential' } }] } }),
    searchServers: async params => {
      calls.push(params)
      return { data: { results: [], total: 0 } }
    },
  })
  setSearchResults(view, refObj, { name: '节点', ipType: 'idc' })
  view.appliedSearchFilters.value = { ...view.emptySearchFilters(), ipType: 'idc' }
  view.nodePage.value = 8
  await view.detectIPTypeForRefs([refObj])
  assert.deepEqual(calls, [{ limit: 100, offset: 700, ipType: 'idc' }])
  assert.equal(view.currentNodeTotal.value, 0)
  assert.equal(view.sortedSearchResults.value.length, 0)
  assert.deepEqual(view.selectedRefs.value, [])
})

test('评分低于当前最低分条件后重算筛选结果', async () => {
  const refObj = { type: 'server', index: 0 }
  const calls = []
  const { view } = await setupHome({
    getIPScoreStatus: async () => ({ data: { state: { running: false, refs: [refObj], total: 1, done: 1, failed: 0 } } }),
    queryServers: async () => ({ data: { results: [{ ref: refObj, server: { ipScore: 60 } }] } }),
    searchServers: async params => {
      calls.push(params)
      return { data: { results: [], total: 0 } }
    },
  })
  setSearchResults(view, refObj, { name: '节点', ipScore: 90 })
  view.appliedSearchFilters.value = { ...view.emptySearchFilters(), minScore: '80' }
  await view.pollIPScore()
  assert.deepEqual(calls, [{ limit: 100, offset: 0, minScore: '80' }])
  assert.equal(view.currentNodeTotal.value, 0)
  assert.equal(view.sortedSearchResults.value.length, 0)
})

test('最低分为数值 0 时仍作为有效筛选条件刷新', async () => {
  let calls = 0
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    searchServers: async params => {
      calls += 1
      assert.equal(params.minScore, '0')
      return { data: { results: [{ ref: refObj, server: { ipScore: 0 } }], total: 1 } }
    },
  })
  setSearchResults(view, refObj, { name: '节点' })
  view.appliedSearchFilters.value = { ...view.emptySearchFilters(), minScore: 0 }
  await view.refreshSearchForChangedFields(['minScore'])
  assert.equal(calls, 1)
  assert.equal(view.currentNodeTotal.value, 1)
})

test('评分补载同时得到新的 IP 类型时也重算类型筛选', async () => {
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    getIPScoreStatus: async () => ({ data: { state: { running: false, refs: [refObj] } } }),
    queryServers: async () => ({ data: { results: [{ ref: refObj, server: { ipScore: 80, ipType: 'residential' } }] } }),
    searchServers: async () => ({ data: { results: [], total: 0 } }),
  })
  setSearchResults(view, refObj, { name: '节点', ipType: 'idc' })
  view.appliedSearchFilters.value = { ...view.emptySearchFilters(), ipType: 'idc' }
  await view.pollIPScore()
  assert.equal(view.currentNodeTotal.value, 0)
  assert.equal(view.sortedSearchResults.value.length, 0)
})

test('筛选页的当前 tab 检测只提交可见结果，不提交整个订阅', async () => {
  const calls = []
  const first = { type: 'sub_server', sub: 0, index: 7000 }
  const { view, store } = await setupHome({
    detectServerIPType: async refs => { calls.push(refs) },
    queryServers: async () => ({ data: { results: [] } }),
  })
  store.subscriptions = [{ id: 'sub', serverCount: 10000 }]
  view.activeTab.value = 'sub-0'
  setSearchResults(view, first, { name: '节点' })
  await view.detectIPTypeForCurrentTab()
  assert.deepEqual(calls, [[
    first, { type: 'server', index: 1, sub: 0 },
  ]])
})

test('评分轮询 single-flight，慢请求不会被 interval 叠加', async () => {
  let finish, calls = 0
  const { view } = await setupHome({
    getIPScoreStatus: () => {
      calls += 1
      return new Promise(resolve => { finish = resolve })
    },
  })
  const first = view.pollIPScore()
  const last = view.pollIPScore()
  assert.equal(first, last)
  await Promise.resolve()
  assert.equal(calls, 1)
  finish({ data: { state: { running: true } } })
  await first
  assert.equal(view.ipScoreTask.value.running, true)
})

test('旧评分轮询晚到的 running 状态不能覆盖新任务完成状态', async () => {
  const requests = []
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    getIPScoreStatus: () => new Promise(resolve => { requests.push(resolve) }),
    queryServers: async () => ({ data: { results: [{ ref: refObj, server: { ipScore: 90 } }] } }),
  })
  setSearchResults(view, refObj, { name: '节点', ipScore: 30 })
  view.ipScoreTask.value = { running: true }
  const older = view.pollIPScore()
  await Promise.resolve()
  view.stopIPScorePoll()
  const newer = view.pollIPScore()
  await Promise.resolve()
  requests[1]({ data: { state: { running: false, refs: [refObj], total: 1, done: 1, failed: 0 } } })
  await newer
  requests[0]({ data: { state: { running: true } } })
  await older
  assert.equal(view.ipScoreTask.value.running, false)
  assert.equal(view.sortedSearchResults.value[0].server.ipScore, 90)
  assert.equal(view.ipScorePollTimer, null)
})

test('旧任务补载结束后不能清除新任务的运行标记', async () => {
  let finishQuery, statusCalls = 0
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    getIPScoreStatus: async () => ({ data: { state: statusCalls++ === 0
      ? { running: false, refs: [refObj] } : { running: true } } }),
    queryServers: () => new Promise(resolve => { finishQuery = resolve }),
  })
  const older = view.pollIPScore()
  await new Promise(resolve => setImmediate(resolve))
  view.stopIPScorePoll()
  view.ipScoringRefs.value = { 'server:0:0': true }
  await view.pollIPScore()
  finishQuery({ data: { results: [{ ref: refObj, server: { ipScore: 80 } }] } })
  await older
  assert.equal(view.ipScoreTask.value.running, true)
  assert.equal(view.ipScoringRefs.value['server:0:0'], true)
})

test('出站重命名通知刷新筛选快照，而不是只清空共享缓存', async () => {
  const refObj = { type: 'server', index: 0 }
  const calls = []
  const { view, store } = await setupHome({
    searchServers: async params => {
      calls.push(params)
      return { data: { results: [{ ref: refObj, server: { frontProxy: 'new-outbound' } }], total: 10000 } }
    },
  })
  setSearchResults(view, refObj, { name: '节点', frontProxy: 'old-outbound' })
  view.appliedSearchFilters.value = { ...view.emptySearchFilters(), type: 'vless' }
  store.invalidateNodeDetails()
  await view.onDataMutated()
  assert.equal(calls.length, 1)
  assert.equal(view.sortedSearchResults.value[0].server.frontProxy, 'new-outbound')
  assert.equal(view.currentNodeTotal.value, 10000)
})

test('非筛选视图的变更通知只补载当前分组页', async () => {
  const calls = []
  const refObj = { type: 'server', index: 0 }
  const { view, store } = await setupHome({
    getGroupServers: async (...args) => {
      calls.push(args)
      return { data: { refs: [refObj], results: [{ ref: refObj, server: { frontProxy: 'new-outbound' } }],
        serverCount: 10000, offset: 0 } }
    },
  })
  store.groups = [{ id: 'group', name: 'SERVER', serverCount: 10000, servers: [refObj], serversLoaded: true }]
  store.servers = [{ frontProxy: 'old-outbound' }]
  store.invalidateNodeDetails()
  await view.onDataMutated()
  assert.deepEqual(calls, [['group', 1, 100]])
  assert.equal(store.servers[0].frontProxy, 'new-outbound')
})

test('筛选失败保留已显示的节点，并区别于无匹配结果', async () => {
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    searchServers: async () => { throw new Error('查询失败') },
  })
  setSearchResults(view, refObj, { name: '已有节点' })
  view.appliedSearchFilters.value = { ...view.emptySearchFilters(), type: 'vless' }
  await view.onSearch(false)
  assert.equal(view.searchError.value, '查询失败')
  assert.equal(view.sortedSearchResults.value[0].server.name, '已有节点')
  assert.equal(view.searchLoading.value, false)
})

test('旧分页请求结束不清除新分页请求的 loading 状态', async () => {
  const requests = []
  const { view, store } = await setupHome({
    getGroupServers: () => new Promise(resolve => { requests.push(resolve) }),
  })
  store.groups = [{ id: 'group', name: 'SERVER', serverCount: 0 }]
  const older = view.ensureActiveTabNodes()
  assert.equal(view.nodePageLoading.value, true)
  const newer = view.ensureActiveTabNodes(true)
  requests[0]({ data: { refs: [], results: [], serverCount: 0 } })
  await older
  assert.equal(view.nodePageLoading.value, true)
  requests[1]({ data: { refs: [], results: [], serverCount: 0 } })
  await newer
  assert.equal(view.nodePageLoading.value, false)
  assert.equal(view.nodePageError.value, '')
})

test('测速筛选只在 SSE 关闭并落库后重算一次', async () => {
  const previousWindow = globalThis.window
  globalThis.window = { requestAnimationFrame: () => 1, cancelAnimationFrame: () => {} }
  homeCleanups.push(() => {
    if (previousWindow === undefined) delete globalThis.window
    else globalThis.window = previousWindow
  })
  let finishStream, reloadStarted
  const reloaded = new Promise(resolve => { reloadStarted = resolve })
  const calls = []
  const refObj = { type: 'server', index: 0 }
  const { view } = await setupHome({
    pingNodes: (refs, mode, options) => {
      assert.equal(mode, 'fast')
      options.onEvent('done', { ref: refs[0], latency: 30 })
      return new Promise(resolve => { finishStream = resolve })
    },
    searchServers: async params => {
      calls.push(params)
      reloadStarted()
      return { data: { results: [], total: 0 } }
    },
  })
  setSearchResults(view, refObj, { name: '节点' })
  view.appliedSearchFilters.value = { ...view.emptySearchFilters(), fastProbe: 'unchecked' }
  await view.pingRefsWithProgress([refObj], 'fast')
  assert.equal(calls.length, 0)
  assert.equal(view.sortedSearchResults.value[0].server.fastLatency, 30)
  finishStream()
  await reloaded
  await new Promise(resolve => setImmediate(resolve))
  assert.deepEqual(calls, [{ limit: 100, offset: 0, fastProbe: 'unchecked' }])
  assert.equal(view.sortedSearchResults.value.length, 0)
  assert.equal(view.currentNodeTotal.value, 0)
})
