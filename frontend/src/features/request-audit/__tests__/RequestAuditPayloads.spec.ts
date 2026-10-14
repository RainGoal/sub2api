import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount, type VueWrapper } from '@vue/test-utils'
import RequestAuditPayloads from '../RequestAuditPayloads.vue'
import type { RequestAuditPayload, RequestAuditRecord } from '../types'

const mocks = vi.hoisted(() => ({ payload: vi.fn(), download: vi.fn(), createObjectURL: vi.fn(), revokeObjectURL: vi.fn() }))
vi.mock('../api', () => ({ default: mocks }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ locale: { value: 'en' }, t: (key: string) => key }) }))

const record: RequestAuditRecord = { id: 'audit-one', received_at: '2026-10-15T00:00:00Z', kind: 'http_request', capture_state: 'complete' }
const response = (content = '{"message":"hello"}', overrides: Partial<RequestAuditPayload> = {}): RequestAuditPayload => ({ content, encoding: 'utf8', offset: 0, returned_bytes: new TextEncoder().encode(content).length, part_bytes: new TextEncoder().encode(content).length, has_more: false, capture_state: 'complete', partial: false, redacted: false, ...overrides })
function bodySlice(content: string, offset = 0) {
  const bytes = new TextEncoder().encode(content), slice = bytes.slice(offset, offset + 65536)
  let value: string, encoding: 'utf8' | 'base64' = 'utf8'
  try { value = new TextDecoder('utf-8', { fatal: true }).decode(slice) }
  catch { value = btoa(Array.from(slice, byte => String.fromCharCode(byte)).join('')); encoding = 'base64' }
  return response(value, { encoding, offset, returned_bytes: slice.length, part_bytes: bytes.length, has_more: offset + slice.length < bytes.length })
}
function largePngDataURI() {
  // Add a valid ancillary text chunk to a 1px PNG so its Base64 spans multiple audit pages.
  const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==', 'base64')
  const data = Buffer.from(`Description\0${'x'.repeat(70000)}`)
  const chunk = Buffer.alloc(data.length + 12)
  chunk.writeUInt32BE(data.length); chunk.write('tEXt', 4); data.copy(chunk, 8)
  let crc = 0xffffffff
  for (const byte of chunk.subarray(4, -4)) {
    crc ^= byte
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) ? 0xedb88320 : 0)
  }
  chunk.writeUInt32BE((crc ^ 0xffffffff) >>> 0, chunk.length - 4)
  return `data:image/png;base64,${Buffer.concat([png.subarray(0, -12), chunk, png.subarray(-12)]).toString('base64')}`
}
const wrappers: VueWrapper[] = []
const unfinished: Array<() => void> = []
function pendingRead() {
  let resolve!: (value: RequestAuditPayload) => void
  const promise = new Promise<RequestAuditPayload>(done => { resolve = done })
  unfinished.push(() => resolve(response()))
  return { promise, resolve }
}
function createView(value = record) {
  const wrapper = mount(RequestAuditPayloads, { props: { record: value } })
  wrappers.push(wrapper)
  return wrapper
}

describe('RequestAuditPayloads', () => {
  beforeEach(() => {
    Object.values(mocks).forEach(mock => mock.mockReset())
    mocks.payload.mockImplementation(async () => response())
    mocks.download.mockResolvedValue(new Blob(['original bytes']))
    mocks.createObjectURL.mockReturnValue('blob:local-audit-download')
    vi.stubGlobal('URL', class extends URL {
      static createObjectURL = mocks.createObjectURL
      static revokeObjectURL = mocks.revokeObjectURL
    })
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
  })
  afterEach(async () => {
    wrappers.splice(0).forEach(wrapper => wrapper.unmount())
    unfinished.splice(0).forEach(finish => finish())
    await flushPromises()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('automatically loads all three parts while allowing at most two active reads', async () => {
    const headers = pendingRead(), body = pendingRead()
    mocks.payload.mockReturnValueOnce(headers.promise).mockReturnValueOnce(body.promise)
    const wrapper = createView()
    expect(mocks.payload).toHaveBeenCalledTimes(2)
    expect(wrapper.findAll('[role="status"]')).toHaveLength(3)
    headers.resolve(response('{"header":"value"}', { redacted: true }))
    await flushPromises()
    expect(mocks.payload.mock.calls.map(call => call[1])).toEqual(['headers', 'body', 'trailers'])
    body.resolve(response())
    await flushPromises()
    expect(wrapper.findAll('pre')).toHaveLength(3)
    expect(wrapper.find('[data-test="body-content"]').exists()).toBe(true)
    expect(mocks.payload).toHaveBeenNthCalledWith(1, record.id, 'headers', 0, 65536, false, false)
    expect(wrapper.get('[data-test="payload-panel-headers"]').text()).toContain('admin.requestAudit.redactedHint')
  })

  it('formats complete JSON by default and renders embedded markup only as text', async () => {
    const content = '{"html":"<script>alert(1)</script>","value":1}'
    mocks.payload.mockResolvedValue(response(content))
    const wrapper = createView()
    await flushPromises()
    await wrapper.get('[data-test="body-mode-raw"]').trigger('click')
    expect(wrapper.get('[data-test="content-body"]').text()).toBe(JSON.stringify(JSON.parse(content), null, 2))
    expect(wrapper.find('script').exists()).toBe(false)
    await wrapper.get('[data-test="format-body"]').setValue(false)
    expect(wrapper.get('[data-test="content-body"]').text()).toBe(content)
  })

  it('keeps empty, failed and successful parts independent and retries only the failed part', async () => {
    mocks.payload.mockImplementation(async (_id, part) => {
      if (part === 'body') return response('')
      if (part === 'trailers') throw { reason: 'AUDIT_STORAGE_UNAVAILABLE' }
      return response()
    })
    const wrapper = createView()
    await flushPromises()
    expect(wrapper.get('[data-test="empty-body"]').text()).toBe('admin.requestAudit.payloads.empty')
    expect(wrapper.find('[data-test="content-headers"]').exists()).toBe(true)
    expect(wrapper.get('[data-test="payload-panel-trailers"]').find('[role="alert"]').exists()).toBe(true)
    mocks.payload.mockResolvedValueOnce(response('{}'))
    await wrapper.get('[data-test="retry-trailers"]').trigger('click')
    await flushPromises()
    expect(mocks.payload).toHaveBeenCalledTimes(4)
    expect(mocks.payload).toHaveBeenLastCalledWith(record.id, 'trailers', 0, 65536, false, false)
    expect(wrapper.find('[data-test="content-trailers"]').exists()).toBe(true)
  })

  it('requires explicit partial-prefix access and keeps incomplete data visibly marked', async () => {
    mocks.payload.mockImplementation(async (_id, part, _offset, _limit, _reveal, partial) => {
      if (part === 'body' && !partial) throw { reason: 'AUDIT_PAYLOAD_INCOMPLETE' }
      return response('{"prefix":true}', { partial: part === 'body', capture_state: part === 'body' ? 'partial' : 'complete' })
    })
    const wrapper = createView({ ...record, capture_state: 'partial' })
    await flushPromises()
    expect(wrapper.find('[data-test="content-body"]').exists()).toBe(false)
    await wrapper.get('[data-test="partial-body"]').setValue(true)
    await flushPromises()
    expect(mocks.payload).toHaveBeenLastCalledWith(record.id, 'body', 0, 65536, false, true)
    expect(wrapper.get('[data-test="payload-panel-body"]').text()).toContain('admin.requestAudit.partialHint')
    expect(wrapper.find('[data-test="format-body"]').exists()).toBe(false)
  })

  it('loads previous and next 64 KiB segments without formatting an incomplete JSON page', async () => {
    mocks.payload.mockImplementation(async (_id, part, offset) => part === 'body'
      ? response(offset ? 'tail' : '{}', { offset, returned_bytes: offset ? 4 : 65536, part_bytes: 65540, has_more: !offset })
      : response())
    const wrapper = createView()
    await wrapper.get('[data-test="body-mode-raw"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="format-body"]').exists()).toBe(false)
    await wrapper.get('[data-test="next-body"]').trigger('click')
    await flushPromises()
    expect(mocks.payload).toHaveBeenLastCalledWith(record.id, 'body', 65536, 65536, false, false)
    expect(wrapper.get('[data-test="next-body"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-test="previous-body"]').trigger('click')
    await flushPromises()
    expect(mocks.payload).toHaveBeenLastCalledWith(record.id, 'body', 0, 65536, false, false)
  })

  it('assembles a complete body beyond 64 KiB and handles split UTF-8 before rendering messages', async () => {
    const prefix = '{"messages":[{"role":"user","content":"'
    const body = `${prefix}${'x'.repeat(65535 - prefix.length)}中文消息"}]}`
    mocks.payload.mockImplementation(async (_id, part, offset) => part === 'body' ? bodySlice(body, offset) : response())
    const wrapper = createView()
    await flushPromises()
    expect(mocks.payload.mock.calls.filter(call => call[1] === 'body').map(call => call[2])).toEqual([0, 65536])
    expect(wrapper.get('[data-test="body-message-text"]').text()).toContain('中文消息')
    expect(wrapper.get('[data-test="payload-panel-body"]').text()).not.toContain('admin.requestAudit.binaryHint')
    expect(wrapper.find('[data-test="content-body"]').exists()).toBe(false)
    await wrapper.get('[data-test="body-mode-raw"]').trigger('click')
    expect(wrapper.find('[data-test="content-body"]').exists()).toBe(true)
  })

  it('renders a complete body across pages when another part makes the overall record partial', async () => {
    const body = JSON.stringify({ messages: [{ role: 'user', content: 'x'.repeat(70000) }] })
    mocks.payload.mockImplementation(async (_id, part, offset) => {
      if (part === 'body') return { ...bodySlice(body, offset), capture_state: 'partial' }
      if (part === 'headers') return response('{}', { partial: true, capture_state: 'partial' })
      const unknown: Partial<RequestAuditPayload> = response('{}', { capture_state: 'partial' })
      delete unknown.partial
      return unknown
    })
    const wrapper = createView({ ...record, capture_state: 'partial' })
    await flushPromises()
    expect(mocks.payload.mock.calls.filter(call => call[1] === 'body').map(call => call[2])).toEqual([0, 65536])
    expect(wrapper.get('[data-test="body-message-text"]').text()).toBe('x'.repeat(70000))
    expect(wrapper.get('[data-test="payload-panel-body"]').get('header').text()).toContain('admin.requestAudit.states.complete')
    expect(wrapper.get('[data-test="payload-panel-headers"]').get('header').text()).toContain('admin.requestAudit.states.partial')
    expect(wrapper.get('[data-test="payload-panel-trailers"]').get('header').text()).toContain('admin.requestAudit.states.unknown')
  })

  it.each([true, undefined])('does not mark or render an unconfirmed body as complete (partial=%s)', async partial => {
    const body = JSON.stringify({ messages: [{ role: 'user', content: 'unconfirmed body' }] })
    mocks.payload.mockImplementation(async (_id, part) => {
      if (part !== 'body') return response()
      const payload: Partial<RequestAuditPayload> = response(body)
      if (partial === undefined) delete payload.partial
      else payload.partial = partial
      return payload
    })
    const wrapper = createView()
    await flushPromises()
    expect(wrapper.find('[data-test="body-message"]').exists()).toBe(false)
    expect(wrapper.get('[data-test="content-body"]').text()).toBe(body)
    const header = wrapper.get('[data-test="payload-panel-body"]').get('header').text()
    expect(header).toContain(`admin.requestAudit.states.${partial === true ? 'partial' : 'unknown'}`)
    expect(header).not.toContain('admin.requestAudit.states.complete')
  })

  it('renders an embedded image whose Base64 spans multiple 64 KiB segments', async () => {
    const src = largePngDataURI()
    const body = JSON.stringify({ messages: [{ role: 'user', content: [{ type: 'text', text: 'Describe this image' }, { type: 'image_url', image_url: { url: src } }] }] })
    mocks.payload.mockImplementation(async (_id, part, offset) => part === 'body' ? bodySlice(body, offset) : response())
    const wrapper = createView()
    await flushPromises()
    expect(body.length).toBeGreaterThan(65536)
    expect(wrapper.get('img').attributes('src')).toBe(src)
    expect(wrapper.get('[data-test="body-message-text"]').text()).toBe('Describe this image')
    expect(mocks.payload.mock.calls.filter(call => call[1] === 'body').map(call => call[2])).toEqual([0, 65536])
  })

  it('keeps body continuations within the shared two-read budget', async () => {
    const body = JSON.stringify({ messages: [{ role: 'user', content: 'x'.repeat(70000) }] })
    const headers = pendingRead(), trailers = pendingRead()
    mocks.payload.mockImplementation(async (_id, part, offset) => part === 'headers' ? headers.promise : part === 'trailers' ? trailers.promise : bodySlice(body, offset))
    const wrapper = createView()
    await flushPromises()
    expect(mocks.payload.mock.calls.map(call => call[1])).toEqual(['headers', 'body', 'trailers'])
    headers.resolve(response())
    await flushPromises()
    expect(mocks.payload.mock.calls.map(call => [call[1], call[2]])).toEqual([['headers', 0], ['body', 0], ['trailers', 0], ['body', 65536]])
    expect(wrapper.find('[data-test="body-message-text"]').exists()).toBe(true)
    trailers.resolve(response())
  })

  it('bounds continuation request count even when the server returns abnormally short segments', async () => {
    mocks.payload.mockImplementation(async (_id, part, offset) => part === 'body' ? response('x', { offset, part_bytes: 100, has_more: true }) : response())
    const wrapper = createView()
    await flushPromises()
    expect(mocks.payload.mock.calls.filter(call => call[1] === 'body')).toHaveLength(64)
    expect(wrapper.get('[data-test="body-content-hint"]').text()).toContain('bodyContent.hints.inconsistent')
  })

  it('does not continue reading a body beyond 4 MiB or render a partial body as complete messages', async () => {
    mocks.payload.mockImplementation(async (_id, part) => part === 'body' ? response('prefix', { part_bytes: 4 * 1024 * 1024 + 1, has_more: true }) : response())
    const wrapper = createView()
    await flushPromises()
    expect(mocks.payload.mock.calls.filter(call => call[1] === 'body')).toHaveLength(1)
    expect(wrapper.get('[data-test="body-content-hint"]').text()).toContain('bodyContent.hints.tooLarge')
    expect(wrapper.get('[data-test="content-body"]').text()).toBe('prefix')
    mocks.payload.mockResolvedValueOnce(response('{"messages":[]}', { partial: true, capture_state: 'partial' }))
    await wrapper.get('[data-test="refresh-body"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-test="body-content-hint"]').text()).toContain('bodyContent.hints.partial')
    expect(wrapper.find('[data-test="body-message"]').exists()).toBe(false)
  })

  it('retains the first raw segment when a continuation fails and can retry the full body', async () => {
    const body = JSON.stringify({ messages: [{ role: 'user', content: 'x'.repeat(70000) }] })
    mocks.payload.mockImplementation(async (_id, part, offset) => {
      if (part !== 'body') return response()
      if (offset) throw { reason: 'AUDIT_STORAGE_UNAVAILABLE' }
      return bodySlice(body)
    })
    const wrapper = createView()
    await flushPromises()
    expect(wrapper.get('[data-test="content-body"]').text()).toBe(bodySlice(body).content)
    mocks.payload.mockImplementation(async (_id, part, offset) => part === 'body' ? bodySlice(body, offset) : response())
    await wrapper.get('[data-test="retry-body-content"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[data-test="body-message-text"]').exists()).toBe(true)
  })

  it('does not interpret invalid UTF-8 bytes as complete messages', async () => {
    mocks.payload.mockImplementation(async (_id, part) => part === 'body' ? response('/w==', { encoding: 'base64', returned_bytes: 1, part_bytes: 1 }) : response())
    const wrapper = createView()
    await flushPromises()
    expect(wrapper.get('[data-test="body-content-hint"]').text()).toContain('bodyContent.hints.binary')
    expect(wrapper.get('[data-test="content-body"]').text()).toBe('/w==')
    expect(wrapper.find('[data-test="body-message"]').exists()).toBe(false)
  })

  it('rejects a mismatched continuation and preserves raw paging', async () => {
    const body = JSON.stringify({ messages: [{ role: 'user', content: 'x'.repeat(70000) }] })
    mocks.payload.mockImplementation(async (_id, part, offset) => part === 'body' ? { ...bodySlice(body, offset), offset: offset ? offset + 1 : 0 } : response())
    const wrapper = createView()
    await flushPromises()
    expect(wrapper.get('[data-test="body-content-hint"]').text()).toContain('bodyContent.hints.inconsistent')
    expect(wrapper.find('[data-test="body-message"]').exists()).toBe(false)
    await wrapper.get('[data-test="body-mode-raw"]').trigger('click')
    expect(wrapper.find('[data-test="next-body"]').exists()).toBe(true)
  })

  it('ignores body continuations after switching to raw or changing records', async () => {
    const body = JSON.stringify({ messages: [{ role: 'user', content: 'old message'.repeat(8000) }] })
    const continuation = pendingRead()
    mocks.payload.mockImplementation(async (_id, part, offset) => part === 'body' ? offset ? continuation.promise : bodySlice(body) : response())
    const wrapper = createView()
    await flushPromises()
    await wrapper.get('[data-test="body-mode-raw"]').trigger('click')
    mocks.payload.mockResolvedValue(response('{"messages":[{"role":"user","content":"new record"}]}'))
    await wrapper.setProps({ record: { ...record, id: 'audit-two' } })
    continuation.resolve(bodySlice(body, 65536))
    await flushPromises()
    expect(wrapper.get('[data-test="body-message-text"]').text()).toBe('new record')
    expect(wrapper.text()).not.toContain('old message')
  })

  it('keeps the final manifest and failure reason visible when the payload cannot be read', async () => {
    mocks.payload.mockRejectedValue({ reason: 'AUDIT_PAYLOAD_INCOMPLETE' })
    const wrapper = createView({ ...record, terminal_manifest: { body: { complete: false, expected_chunks: 3, expected_bytes: 100, reason: 'read failed before completion' } } })
    await flushPromises()
    const manifest = wrapper.get('[data-test="manifest-body"]')
    expect(manifest.text()).toContain('admin.requestAudit.states.partial')
    expect(manifest.text()).toContain('admin.requestAudit.chunks')
    expect(manifest.text()).toContain('admin.requestAudit.payloads.manifestBytes')
    expect(manifest.text()).toContain('read failed before completion')
    expect(wrapper.get('[data-test="manifest-trailers"]').text()).toBe('admin.requestAudit.manifestUnavailable')
  })

  it('does not format binary slices and explains their Base64 encoding', async () => {
    mocks.payload.mockResolvedValue(response('AAECAw==', { encoding: 'base64' }))
    const wrapper = createView()
    await flushPromises()
    await wrapper.get('[data-test="body-mode-raw"]').trigger('click')
    expect(wrapper.get('[data-test="content-body"]').text()).toBe('AAECAw==')
    expect(wrapper.get('[data-test="payload-panel-body"]').text()).toContain('admin.requestAudit.binaryHint')
    expect(wrapper.find('[data-test="format-body"]').exists()).toBe(false)
  })

  it('drops a delayed sensitive reveal after the administrator hides headers again', async () => {
    const sensitive = pendingRead()
    const wrapper = createView()
    await flushPromises()
    mocks.payload.mockReturnValueOnce(sensitive.promise)
    await wrapper.get('[data-test="reveal-headers"]').setValue(true)
    await wrapper.get('[data-test="reveal-headers"]').setValue(false)
    await flushPromises()
    sensitive.resolve(response('{"Authorization":"old-secret"}'))
    await flushPromises()
    expect(wrapper.text()).not.toContain('old-secret')
    expect(mocks.payload).toHaveBeenLastCalledWith(record.id, 'headers', 0, 65536, false, false)
  })

  it('skips old queued reads and ignores old results when the record changes', async () => {
    const headers = pendingRead(), body = pendingRead()
    mocks.payload.mockReturnValueOnce(headers.promise).mockReturnValueOnce(body.promise)
    const wrapper = createView()
    await wrapper.setProps({ record: { ...record, id: 'audit-two' } })
    expect(mocks.payload).toHaveBeenCalledTimes(2)
    headers.resolve(response('old headers')); body.resolve(response('old body'))
    await flushPromises()
    expect(mocks.payload.mock.calls.slice(2).map(call => [call[0], call[1]])).toEqual([['audit-two', 'headers'], ['audit-two', 'body'], ['audit-two', 'trailers']])
    expect(wrapper.text()).not.toContain('old headers')
    expect(wrapper.text()).not.toContain('old body')
  })

  it('retains the two-read budget across close and reopen', async () => {
    const headers = pendingRead(), body = pendingRead()
    mocks.payload.mockReturnValueOnce(headers.promise).mockReturnValueOnce(body.promise)
    const first = createView()
    first.unmount()
    const reopened = createView({ ...record, id: 'audit-two' })
    expect(mocks.payload).toHaveBeenCalledTimes(2)
    headers.resolve(response()); body.resolve(response())
    await flushPromises()
    expect(mocks.payload).toHaveBeenCalledTimes(5)
    expect(reopened.findAll('pre')).toHaveLength(3)
  })

  it('does not replace a newer preview with an older download failure', async () => {
    const wrapper = createView()
    await flushPromises()
    let failDownload!: (reason: unknown) => void
    mocks.download.mockReturnValueOnce(new Promise<Blob>((_resolve, reject) => { failDownload = reject }))
    await wrapper.get('[data-test="download-headers"]').trigger('click')
    await wrapper.get('[data-test="reveal-headers"]').setValue(true)
    await flushPromises()
    failDownload({ reason: 'AUDIT_DOWNLOAD_INCOMPLETE' })
    await flushPromises()
    expect(wrapper.get('[data-test="payload-panel-headers"]').find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.find('[data-test="content-headers"]').exists()).toBe(true)
  })

  it('queues raw downloads behind active previews and never saves a download after close', async () => {
    const wrapper = createView()
    await flushPromises()
    const headers = pendingRead(), body = pendingRead()
    mocks.payload.mockReturnValueOnce(headers.promise).mockReturnValueOnce(body.promise)
    await wrapper.get('[data-test="refresh-headers"]').trigger('click')
    await wrapper.get('[data-test="refresh-body"]').trigger('click')
    await wrapper.get('[data-test="download-trailers"]').trigger('click')
    expect(mocks.download).not.toHaveBeenCalled()
    headers.resolve(response())
    await flushPromises()
    expect(mocks.download).toHaveBeenCalledWith(record.id, 'trailers', false)
    expect(mocks.createObjectURL).toHaveBeenCalledOnce()
    body.resolve(response())
    await flushPromises()

    let finishDownload!: (value: Blob) => void
    mocks.download.mockReturnValueOnce(new Promise<Blob>(resolve => { finishDownload = resolve }))
    await wrapper.get('[data-test="download-body"]').trigger('click')
    wrapper.unmount()
    finishDownload(new Blob(['stale']))
    await flushPromises()
    expect(mocks.createObjectURL).toHaveBeenCalledOnce()
  })
})
