import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test, { afterEach } from 'node:test'
import { compileScript, parse } from 'vue/compiler-sfc'
import { renderToString } from 'vue/server-renderer'
import * as vue from 'vue'

const componentPaths = {
  home: '../src/views/HomeView.vue',
  app: '../src/App.vue',
  rules: '../src/views/RulesView.vue',
  settings: '../src/views/SettingsView.vue',
}
const componentSources = Object.fromEntries(Object.entries(componentPaths).map(([name, path]) =>
  [name, readFileSync(new URL(path, import.meta.url), 'utf8')]
))
const componentScripts = Object.fromEntries(Object.entries(componentSources).map(([name, source]) => {
  const { descriptor } = parse(source)
  return [name, compileScript(descriptor, { id: `destructive-actions-${name}` }).content]
}))
const viewCleanups = []
const nodeRef = { type: 'server', index: 7 }
const selectedRefs = [nodeRef, { type: 'server', index: 9 }]
const initialRules = [
  { id: 'keep-rule', name: '保留规则', enabled: true, action: 'direct' },
  { id: 'target-rule', name: '待删除规则', enabled: true, action: 'block' },
]
const initialInbounds = [
  { id: 'builtin-target', name: '待删除代理', protocol: 'cfdo', port: 1081 },
  { id: 'inbound-target', name: '待删除入站', protocol: 'socks', tag: 'test-in', port: 1082 },
  { id: 'inbound-keep', name: '保留入站', protocol: 'http', port: 1083 },
]
const initialInboundSubscriptions = [{ id: 'online-sub', name: '在线订阅', host: 'example.com' }]
const writeMethods = new Set([
  'deleteSubscription', 'deleteGroup', 'deleteServers', 'removeServerFromGroup',
  'removeServersFromGroup', 'deleteOutbound', 'deleteInboundSubscription',
  'setRoutingRules', 'setCustomInbounds',
])

afterEach(() => {
  for (const cleanup of viewCleanups.splice(0)) cleanup()
})

function clone(value) {
  return JSON.parse(JSON.stringify(value))
}

function snapshot({ view, store }) {
  const fields = [
    'activeTab', 'selectedRefs', 'searchResults', 'searchTotal', 'searchLoading',
    'rules', 'inbounds', 'inboundSubscriptions', 'form', 'currentOutbound', 'showOutboundMenu',
  ]
  return clone({
    store,
    view: Object.fromEntries(fields.filter(field => field in view).map(field =>
      [field, vue.unref(view[field])]
    )),
  })
}

async function setupView(name, { confirmed = false, failWrite = false, searching = false } = {}) {
  const calls = []
  const backend = {
    rules: clone(initialRules),
    inbounds: clone(initialInbounds),
    inboundSubscriptions: clone(initialInboundSubscriptions),
  }
  const failure = new Error('模拟写入失败')
  const api = new Proxy({}, {
    get(target, method) {
      return async (...args) => {
        calls.push({ kind: 'api', method, args: clone(args) })
        if (failWrite && writeMethods.has(method)) throw failure
        if (method === 'setRoutingRules') backend.rules = clone(args[0])
        if (method === 'setCustomInbounds') backend.inbounds = clone(args[0])
        if (method === 'deleteInboundSubscription') backend.inboundSubscriptions.splice(args[0], 1)
        if (method === 'getRoutingRules') return { data: { rules: clone(backend.rules) } }
        if (method === 'getCustomInbounds') return { data: { inbounds: clone(backend.inbounds) } }
        if (method === 'getBuiltinProxyStatuses') return { data: { statuses: [] } }
        if (method === 'getInboundSubscriptions') {
          return { data: { subscriptions: clone(backend.inboundSubscriptions) } }
        }
        return { data: { groupId: 'target-group', groupServerCount: 9998 } }
      }
    },
  })
  const store = vue.reactive({
    servers: [], subscriptions: [], groups: [], outbounds: [], customInbounds: [],
    setting: {}, running: true,
  })
  for (const method of [
    'invalidateSubscriptions', 'invalidateNodeDetails', 'fetchSubscriptions', 'fetchGroups',
    'fetchOutbounds', 'fetchCustomInbounds', 'fetchGroupServers', 'fetchSubscriptionServers',
  ]) {
    store[method] = (...args) => {
      calls.push({ kind: 'store', method, args: clone(args) })
      return Promise.resolve()
    }
  }
  store.patchGroupCount = (groupId, count) => {
    calls.push({ kind: 'store', method: 'patchGroupCount', args: [groupId, count] })
    const group = store.groups.find(candidate => candidate.id === groupId)
    if (group) group.serverCount = count
  }
  class MockFormData {
    fields = []
    append(field, value) {
      this.fields.push([field, value])
    }
  }
  const bindings = {
    ref: vue.ref, reactive: vue.reactive, computed: vue.computed, inject: vue.inject,
    watch: vue.watch, onMounted: vue.onMounted, onBeforeUnmount: vue.onBeforeUnmount,
    provide: vue.provide, nextTick: vue.nextTick,
    useProxyStore: () => store,
    useRouter: () => ({ currentRoute: vue.ref({ path: '/' }) }),
    api, setUnauthorizedHandler: () => {},
    ServerRow: null, ServerListHead: null, NodeFilterPanel: null, NodeMenu: null,
    ImportModal: null, RulesView: null, axios: {},
    localStorage: { getItem: () => null },
    confirm: message => {
      calls.push({ kind: 'confirm', message })
      return confirmed
    },
    alert: message => { calls.push({ kind: 'alert', message }) },
    FormData: MockFormData,
    fetch: async (url, options) => {
      calls.push({ kind: 'fetch', url, options })
      if (failWrite) throw failure
      backend.rules = [{ id: 'imported-rule', name: '导入规则', enabled: true, action: 'direct' }]
      return { ok: true, json: async () => ({ count: 1 }) }
    },
  }
  const factory = new Function(
    ...Object.keys(bindings),
    componentScripts[name].replace(/^import .*$/gm, '').replace('export default', 'return'),
  )
  const component = factory(...Object.values(bindings))
  let view
  const app = vue.createSSRApp({
    setup(props, setupContext) {
      view = component.setup({ mode: 'all' }, setupContext)
      return () => null
    },
  })
  app.provide('openImport', () => {})
  app.provide('currentOutbound', vue.ref('proxy'))
  await renderToString(app)
  viewCleanups.push(() => {
    clearTimeout(view.noticeTimer)
    view.stopIPScorePoll?.()
  })
  store.servers[7] = { name: '待删除节点', host: 'example.com', port: 443 }
  store.servers[9] = { name: '另一节点', host: 'example.net', port: 443 }
  store.groups = [
    { id: 'target-group', name: '目标分组', fromSub: false, serverCount: 10000, servers: clone(selectedRefs) },
    { id: 'subscription-group', name: '订阅分组', fromSub: true, serverCount: 10000 },
    { id: 'builtin-group', name: 'SERVER', fromSub: false, serverCount: 0 },
  ]
  store.subscriptions = [{
    id: 'subscription', name: '源订阅', host: 'example.com', serverCount: 10000,
    servers: [{ name: '仅加载的一个节点' }],
  }]
  store.outbounds = [{ name: 'proxy', target: {} }, { name: '测试出站', target: {} }]
  if (name === 'home') {
    view.activeTab.value = 'group-1'
    view.selectedRefs.value = clone(selectedRefs)
    view.isSearching.value = searching
    view.searchTotal.value = 10000
    view.searchResults.value = [{ ref: clone(nodeRef), server: clone(store.servers[7]) }]
  }
  if (name === 'app') {
    view.currentOutbound.value = '测试出站'
    view.showOutboundMenu.value = true
  }
  if (name === 'rules') {
    view.rules.value = clone(initialRules)
    view.inbounds.value = clone(initialInbounds)
    view.inboundSubscriptions.value = clone(initialInboundSubscriptions)
  }
  if (name === 'settings') {
    view.form.nodeCleanupRules = [
      { groupIds: ['target-group'], mode: 'real', intervalHour: 24, recycleGroupId: '' },
      { groupIds: ['builtin-group'], mode: 'fast', intervalHour: 48, recycleGroupId: '' },
    ]
    view.form.subscriptionBestNodeCopyRules = [
      { sourceGroupIds: ['subscription-group'], targetGroupIds: ['target-group'], mode: 'best', count: 0 },
      { sourceGroupIds: ['subscription-group'], targetGroupIds: ['builtin-group'], mode: 'all', count: 0 },
    ]
  }
  calls.length = 0
  const fileEvent = { target: { files: [{ name: 'routing-rules.json' }], value: 'routing-rules.json' } }
  return { view, store, calls, failure, fileEvent }
}

const actionCases = [
  { label: '源订阅删除', page: 'home', method: 'deleteSub', args: [0], apiMethod: 'deleteSubscription',
    apiArgs: [0], message: /源订阅/, impact: /10000 个源节点.*自动创建的分组/ },
  { label: '分组删除', page: 'home', method: 'deleteGroup', args: [1], apiMethod: 'deleteGroup',
    apiArgs: [0], message: /目标分组/, impact: /10000 个节点/ },
  { label: '手动节点删除', page: 'home', method: 'removeManualServer', args: [nodeRef], apiMethod: 'deleteServers',
    apiArgs: [[7]], message: /待删除节点/, impact: /出站绑定/ },
  { label: '分组单节点移除', page: 'home', method: 'removeFromGroup', args: [0, nodeRef],
    apiMethod: 'removeServerFromGroup', apiArgs: [0, nodeRef], message: /目标分组.*待删除节点/,
    impact: /其他分组中的独立副本不受影响/ },
  { label: '分组批量节点移除', page: 'home', method: 'removeSelected', args: [],
    apiMethod: 'removeServersFromGroup', apiArgs: [0, selectedRefs], message: /目标分组.*2 个节点/,
    impact: /其他分组中的独立副本不受影响/ },
  { label: '出站删除', page: 'app', method: 'deleteOutbound', args: ['测试出站'],
    apiMethod: 'deleteOutbound', apiArgs: ['测试出站'], message: /测试出站/, impact: /节点和分组不会被删除/ },
  { label: '路由规则删除', page: 'rules', method: 'deleteRule', args: [1],
    apiMethod: 'setRoutingRules', apiArgs: [[initialRules[0]]], message: /待删除规则/, impact: /流量分流/ },
  { label: '入站删除', page: 'rules', method: 'deleteInbound', args: [0],
    apiMethod: 'setCustomInbounds', apiArgs: [[initialInbounds[0], initialInbounds[2]]],
    message: /待删除入站/, impact: /停止监听.*在线订阅/ },
  { label: '内置代理节点删除', page: 'rules', method: 'deleteBuiltinProxy', args: [0],
    apiMethod: 'setCustomInbounds', apiArgs: [[initialInbounds[1], initialInbounds[2]]],
    message: /待删除代理/, impact: /自动生成的节点会被移除/ },
  { label: '在线订阅删除', page: 'rules', method: 'deleteInboundSubscription', args: [0],
    apiMethod: 'deleteInboundSubscription', apiArgs: [0], message: /在线订阅/, impact: /订阅链接将立即失效/ },
  { label: '路由规则覆盖导入', page: 'rules', method: 'importRules',
    args: harness => [harness.fileEvent], message: /routing-rules.json/, impact: /全部被替换（不是追加导入）/ },
  { label: '节点清理规则删除', page: 'settings', method: 'removeCleanupRule', args: [0],
    draftField: 'nodeCleanupRules', message: /第 1 条节点清理规则/, impact: /保存设置后生效.*不会删除节点/ },
  { label: '节点自动复制规则删除', page: 'settings', method: 'removeCopyRule', args: [0],
    draftField: 'subscriptionBestNodeCopyRules', message: /第 1 条节点自动复制规则/,
    impact: /保存设置后生效.*已复制的节点不受影响/ },
]

function invokeAction(harness, action) {
  const args = typeof action.args === 'function' ? action.args(harness) : action.args
  return harness.view[action.method](...args)
}

for (const action of actionCases) {
  test(`${action.label}：取消确认不发请求、不改变已有数据或选择`, async () => {
    const harness = await setupView(action.page, { searching: true })
    const originalState = snapshot(harness)

    await invokeAction(harness, action)

    assert.equal(harness.calls.length, 1)
    assert.equal(harness.calls[0].kind, 'confirm')
    assert.match(harness.calls[0].message, action.message)
    assert.match(harness.calls[0].message, action.impact)
    assert.deepEqual(snapshot(harness), originalState)
    if (action.method === 'importRules') assert.equal(harness.fileEvent.target.value, '')
  })

  test(`${action.label}：确认后才执行，且只操作目标对象`, async () => {
    const harness = await setupView(action.page, { confirmed: true })
    const originalDraft = action.draftField ? clone(harness.view.form[action.draftField]) : null

    await invokeAction(harness, action)

    assert.equal(harness.calls[0].kind, 'confirm')
    assert.equal(harness.calls.filter(call => call.kind === 'confirm').length, 1)
    const writes = harness.calls.filter(call => call.kind === 'api' && writeMethods.has(call.method))
    if (action.apiMethod) {
      assert.deepEqual(writes, [{ kind: 'api', method: action.apiMethod, args: action.apiArgs }])
    } else if (action.draftField) {
      assert.deepEqual(harness.calls.map(call => call.kind), ['confirm'])
      assert.deepEqual(clone(harness.view.form[action.draftField]), originalDraft.slice(1))
    } else {
      assert.deepEqual(writes, [])
      const requests = harness.calls.filter(call => call.kind === 'fetch')
      assert.equal(requests.length, 1)
      assert.equal(requests[0].url, '/api/routing-rules/import')
      assert.equal(requests[0].options.method, 'POST')
      assert.deepEqual(requests[0].options.body.fields, [['file', harness.fileEvent.target.files[0]]])
      assert.equal(harness.fileEvent.target.value, '')
    }
    if (action.method === 'removeSelected') assert.deepEqual(harness.view.selectedRefs.value, [])
    if (action.method === 'deleteOutbound') {
      assert.equal(harness.view.currentOutbound.value, 'proxy')
      assert.equal(harness.view.showOutboundMenu.value, false)
    }
    if (action.method === 'deleteRule') assert.deepEqual(clone(harness.view.rules.value), [initialRules[0]])
    if (action.method === 'deleteInbound' || action.method === 'deleteBuiltinProxy') {
      assert.deepEqual(harness.view.inbounds.value.map(inbound => inbound.id), action.apiArgs[0].map(inbound => inbound.id))
    }
    if (action.method === 'deleteInboundSubscription') assert.deepEqual(harness.view.inboundSubscriptions.value, [])
  })

  if (!action.draftField) {
    test(`${action.label}：提交失败保留列表和选择，不执行后续刷新`, async () => {
      const harness = await setupView(action.page, { confirmed: true, failWrite: true })
      const originalState = snapshot(harness)

      await Promise.resolve(invokeAction(harness, action)).catch(error => {
        assert.equal(error, harness.failure)
      })

      assert.equal(harness.calls[0].kind, 'confirm')
      assert.deepEqual(snapshot(harness), originalState)
      assert.equal(harness.calls.filter(call => call.kind === 'store').length, 0)
      assert.equal(harness.calls.filter(call => call.kind === 'api' && !writeMethods.has(call.method)).length, 0)
    })
  }
}

const invalidActions = [
  { page: 'home', method: 'deleteSub', args: [99] },
  { page: 'home', method: 'deleteGroup', args: [99] },
  { page: 'home', method: 'deleteGroup', args: [0] },
  { page: 'home', method: 'removeFromGroup', args: [99, nodeRef] },
  { page: 'home', method: 'removeManualServer', args: [null] },
  { page: 'home', method: 'removeManualServer', args: [{ type: 'sub_server', sub: 0, index: 0 }] },
  { page: 'app', method: 'deleteOutbound', args: ['proxy'] },
  { page: 'rules', method: 'deleteRule', args: [99] },
  { page: 'rules', method: 'deleteInbound', args: [99] },
  { page: 'rules', method: 'deleteBuiltinProxy', args: [99] },
  { page: 'rules', method: 'deleteInboundSubscription', args: [99] },
  { page: 'settings', method: 'removeCleanupRule', args: [99] },
  { page: 'settings', method: 'removeCopyRule', args: [99] },
]

test('不存在的删除目标和内置分组/出站不弹确认，也不发送请求', async () => {
  for (const action of invalidActions) {
    const harness = await setupView(action.page, { confirmed: true })
    const originalState = snapshot(harness)

    await invokeAction(harness, action)

    assert.deepEqual(harness.calls, [], `${action.page}.${action.method}`)
    assert.deepEqual(snapshot(harness), originalState)
  }
})

test('批量移除没有选择或位于订阅页时保持原状', async () => {
  for (const subscriptionTab of [false, true]) {
    const harness = await setupView('home', { confirmed: true })
    if (subscriptionTab) harness.view.activeTab.value = 'sub-0'
    else harness.view.selectedRefs.value = []
    const originalState = snapshot(harness)

    await harness.view.removeSelected()

    assert.deepEqual(harness.calls, [])
    assert.deepEqual(snapshot(harness), originalState)
  }
})

test('订阅/分组确认只读取已有摘要总数，不补载节点详情', async () => {
  const harness = await setupView('home')
  harness.store.subscriptions[0].name = ''
  harness.store.subscriptions[0].servers = undefined
  harness.store.groups[0].servers = undefined

  await harness.view.deleteSub(0)
  await harness.view.deleteGroup(1)

  assert.deepEqual(harness.calls.map(call => call.kind), ['confirm', 'confirm'])
  assert.match(harness.calls[0].message, /example.com/)
  assert.match(harness.calls[0].message, /10000 个源节点/)
  assert.match(harness.calls[1].message, /10000 个节点/)
})

test('空分组删除仍需确认，提示真实的零节点数量', async () => {
  const harness = await setupView('home')
  harness.store.groups[0].serverCount = 0
  harness.store.groups[0].servers = []

  await harness.view.deleteGroup(1)

  assert.deepEqual(harness.calls.map(call => call.kind), ['confirm'])
  assert.match(harness.calls[0].message, /0 个节点/)
})

test('未选择导入文件时不确认、不覆盖规则', async () => {
  const harness = await setupView('rules', { confirmed: true })
  const originalState = snapshot(harness)

  await harness.view.importRules({ target: { files: [] } })

  assert.deepEqual(harness.calls, [])
  assert.deepEqual(snapshot(harness), originalState)
})

test('出站删除按钮不在确认方法外提前关闭菜单', () => {
  assert.match(componentSources.app, /@click\.stop="deleteOutbound\(o\.name\)"/)
  assert.doesNotMatch(componentSources.app, /@click\.stop="deleteOutbound\(o\.name\);/)
})

test('所有直接删除 API 调用均纳入确认行为测试', () => {
  const discovered = []
  for (const [page, source] of Object.entries(componentSources)) {
    for (const match of source.matchAll(/api\.((?:delete|remove)[A-Z]\w*)\(/g)) {
      discovered.push(`${page}:${match[1]}`)
    }
  }
  const covered = actionCases.filter(action => action.apiMethod?.match(/^(delete|remove)/))
    .map(action => `${action.page}:${action.apiMethod}`)

  assert.deepEqual(discovered.sort(), covered.sort())
})

test('编辑器内移除账号、用户或监听仅修改草稿，不立即提交删除', async () => {
  for (const method of ['removeInboundAccount', 'removeVMessUser', 'removeCfdoListener', 'removeBuiltinAccount']) {
    const harness = await setupView('rules')
    const persistedInbounds = clone(harness.view.inbounds.value)
    harness.view.inboundForm.value.accounts = [{ username: 'user', password: 'test-password' }]
    harness.view.inboundForm.value.vmess.users = [{ uuid: 'test-uuid', name: 'user' }]
    harness.view.inboundForm.value.cfdo.listeners = [{ listenPort: 1234, workerIp: '1.1.1.1' }]
    harness.view.builtinForm.accounts = [{ username: 'user', password: 'test-password' }]

    harness.view[method](0)

    assert.deepEqual(harness.calls, [])
    assert.deepEqual(clone(harness.view.inbounds.value), persistedInbounds)
  }
})

test('清空日志仅清理当前显示，不发送后端清理请求', async () => {
  const harness = await setupView('app')
  harness.view.logs.value = [{ message: '测试日志' }]

  harness.view.clearLogs()

  assert.deepEqual(harness.calls, [])
  assert.deepEqual(harness.view.logs.value, [])
})
