import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { compileScript, parse } from 'vue/compiler-sfc'
import { renderToString } from 'vue/server-renderer'
import * as vue from 'vue'

const source = readFileSync(new URL('../src/views/SettingsView.vue', import.meta.url), 'utf8')
const { descriptor } = parse(source)
const script = compileScript(descriptor, { id: 'settings-cleanup-test' }).content
const createSettings = new Function(
  'ref', 'reactive', 'onMounted', 'computed', 'api', 'useProxyStore', 'RulesView', 'setTimeout',
  script.replace(/^import .*$/gm, '').replace('export default', 'return'),
)

async function setupSettings(setting = {}) {
  const writes = []
  let savedSetting = setting
  const api = {
    getSetting: async () => ({ data: { setting: savedSetting } }),
    setSetting: async payload => {
      savedSetting = JSON.parse(JSON.stringify(payload))
      writes.push(savedSetting)
      return { data: {} }
    },
  }
  const component = createSettings(
    vue.ref, vue.reactive, vue.onMounted, vue.computed, api,
    () => ({ fetchStatus: async () => {} }), null, () => {},
  )
  let view
  const app = vue.createSSRApp({
    setup(props, context) {
      view = component.setup(props, context)
      return () => null
    },
  })
  await renderToString(app)
  await view.loadSetting()
  view.groups.value = [
    { id: 'source', fromSub: false },
    { id: 'recycle', fromSub: false },
    { id: 'subscription', fromSub: true },
  ]
  return { view, writes }
}

test('new cleanup rules default to a transit threshold of 85', async () => {
  const { view } = await setupSettings()
  view.addCleanupRule()
  assert.equal(view.form.nodeCleanupRules[0].transitScoreThreshold, 85)
  const legacy = view.normalizeCleanupRules({
    nodeCleanupGroupIds: ['source'], nodeCleanupMode: 'fast',
  })
  assert.equal(legacy[0].mode, 'fast')
  assert.equal(legacy[0].transitScoreThreshold, 85)
})

test('transit mode and threshold survive loading, saving and reloading', async () => {
  for (const threshold of [undefined, 0, 90, 100]) {
    const { view, writes } = await setupSettings({
      nodeCleanupRules: [{
        groupIds: ['source'], mode: 'transit', intervalHour: 12,
        recycleGroupId: 'recycle', transitScoreThreshold: threshold,
      }],
    })
    assert.equal(view.form.nodeCleanupRules[0].mode, 'transit')
    assert.equal(view.form.nodeCleanupRules[0].transitScoreThreshold, threshold ?? 85)
    await view.saveSettings()
    assert.equal(writes.length, 1)
    assert.equal(writes[0].nodeCleanupRules[0].mode, 'transit')
    assert.equal(writes[0].nodeCleanupRules[0].transitScoreThreshold, threshold ?? 85)
    await view.loadSetting()
    assert.equal(view.form.nodeCleanupRules[0].transitScoreThreshold, threshold ?? 85)
  }
})

test('invalid transit thresholds block saving without losing the draft', async () => {
  for (const threshold of [-1, 101, 85.5, '', '85', NaN]) {
    const { view, writes } = await setupSettings()
    view.form.nodeCleanupRules = [{
      groupIds: ['source'], mode: 'transit', transitScoreThreshold: threshold,
    }]
    await view.saveSettings()
    assert.equal(writes.length, 0)
    assert.equal(view.saving.value, false)
    assert.equal(view.saveMsg.value.error, true)
    assert.match(view.saveMsg.value.text, /0 到 100/)
    assert.equal(view.form.nodeCleanupRules[0].transitScoreThreshold, threshold)
  }
})

test('threshold is only submitted for transit rules', async () => {
  const { view } = await setupSettings()
  for (const mode of ['fast', 'real', 'realstrict']) {
    const rules = view.sanitizeCleanupRules([{
      groupIds: ['source'], mode, transitScoreThreshold: 90,
    }])
    assert.equal(rules[0].mode, mode)
    assert.equal('transitScoreThreshold' in rules[0], false)
  }
})

test('transit threshold control is conditional and explains strict score matching', () => {
  assert.match(descriptor.template.content, /<option value="transit">中转失败才清理<\/option>/)
  assert.match(descriptor.template.content, /<template v-if="rule\.mode === 'transit'">/)
  assert.match(descriptor.template.content, /v-model\.number="rule\.transitScoreThreshold" min="0" max="100" step="1"/)
  assert.match(descriptor.template.content, /评分高于此值才重试中转/)
})

test('cleanup source choices and saved rules exclude subscription groups', async () => {
  const { view, writes } = await setupSettings({
    nodeCleanupRules: [
      { groupIds: ['source', 'subscription'], mode: 'transit', transitScoreThreshold: 90 },
      { groupIds: ['subscription'], mode: 'fast' },
    ],
  })
  assert.deepEqual(view.localGroups.value.map(group => group.id), ['source', 'recycle'])
  assert.match(descriptor.template.content, /v-for="g in localGroups" :key="g\.id" class="cleanup-group-item"/)
  await view.saveSettings()
  assert.equal(writes.length, 1)
  assert.equal(writes[0].nodeCleanupRules.length, 1)
  assert.deepEqual(writes[0].nodeCleanupRules[0].groupIds, ['source'])
})
