import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import RequestAuditBodyContent from '../RequestAuditBodyContent.vue'
import type { RequestAuditPayload } from '../types'

vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
const png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg=='
function render(value: unknown) {
  const content = typeof value === 'string' ? value : JSON.stringify(value)
  const bytes = new TextEncoder().encode(content).length
  const fallback: RequestAuditPayload = { content, encoding: 'utf8', offset: 0, returned_bytes: bytes, part_bytes: bytes, has_more: false, capture_state: 'complete', partial: false, redacted: false }
  return mount(RequestAuditBodyContent, { props: { content, fallback } })
}

describe('RequestAuditBodyContent', () => {
  it('shows message roles, safe literal text and embedded raster images', () => {
    const wrapper = render({ messages: [{ role: 'user', content: [{ type: 'text', text: '<script>alert(1)</script>' }, { type: 'image_url', image_url: { url: `data:image/png;base64,${png}` } }] }] })
    expect(wrapper.get('[data-test="body-message-text"]').text()).toBe('<script>alert(1)</script>')
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.get('img').attributes('src')).toBe(`data:image/png;base64,${png}`)
    expect(wrapper.text()).toContain('admin.requestAudit.bodyContent.roles.user')
    wrapper.unmount()
  })

  it('never creates an image or link for external URLs and reports unknown blocks', () => {
    const wrapper = render({ messages: [{ role: 'user', content: [{ type: 'image_url', image_url: { url: 'https://example.test/private-image' } }, { type: 'unknown_input' }] }] })
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.find('a').exists()).toBe(false)
    expect(wrapper.get('[data-test="body-image-unavailable"]').text()).toContain('imageReasons.external')
    expect(wrapper.find('[data-test="body-block-unsupported"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('automatically falls back to formatted raw content for unrecognized JSON', async () => {
    const content = '{"model":"example","other":"value"}'
    const wrapper = render(content)
    expect(wrapper.get('[data-test="content-body"]').text()).toBe(JSON.stringify(JSON.parse(content), null, 2))
    await wrapper.get('[data-test="format-body"]').setValue(false)
    expect(wrapper.get('[data-test="content-body"]').text()).toBe(content)
    wrapper.unmount()
  })

  it('replaces an undecodable image with a visible failure message', async () => {
    const wrapper = render({ messages: [{ role: 'user', content: [{ type: 'image_url', image_url: { url: `data:image/png;base64,${png}` } }] }] })
    await wrapper.get('img').trigger('error')
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toContain('imageReasons.invalid')
    wrapper.unmount()
  })
})
