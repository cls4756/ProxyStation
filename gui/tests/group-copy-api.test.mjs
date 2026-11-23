import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import test from 'node:test'
import { runInNewContext } from 'node:vm'

const source = await readFile(new URL('../src/api/index.js', import.meta.url), 'utf8')

for (const method of ['copyServerToGroup', 'batchCopyServersToGroup']) {
  test(`${method} 不再发送共享引用参数，旧第三参数也不能启用共享`, async () => {
    const line = source.split(/\r?\n/).find(value => value.trim().startsWith(`${method}:`))
    assert.ok(line)
    const definition = line.trim().slice(method.length + 1).replace(/,$/, '').trim()
    let request
    const copy = runInNewContext(`(${definition})`, {
      http: {
        post: async (path, body) => {
          request = JSON.parse(JSON.stringify({ path, body }))
        },
      },
    })
    const reference = { type: 'server', index: 7 }
    const refs = method === 'copyServerToGroup' ? reference : [reference]
    await copy(refs, 'target-group', false)
    assert.equal(request.body.groupId, 'target-group')
    assert.equal(Object.hasOwn(request.body, 'clone'), false)
    assert.deepEqual(request.body[method === 'copyServerToGroup' ? 'ref' : 'refs'], refs)
  })
}
