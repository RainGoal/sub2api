import { describe, expect, it } from 'vitest'
import { normalizeCommunityContact, validateCommunityContact } from '../communityContact'

describe('community contact settings validation', () => {
  it('fills missing legacy fields and trims text without borrowing defaults', () => {
    const empty = {
      group_name: { 'zh-CN': '', 'en-US': '' },
      description: { 'zh-CN': '', 'en-US': '' },
      group_number: '', invite_url: '', qr_image_url: '',
    }
    expect(normalizeCommunityContact()).toEqual(empty)
    expect(normalizeCommunityContact(null)).toEqual(empty)
    expect(normalizeCommunityContact({ group_name: { 'zh-CN': '  测试群  ' } })).toEqual({
      ...empty, group_name: { 'zh-CN': '测试群', 'en-US': '' },
    })
    expect(validateCommunityContact(normalizeCommunityContact())).toBeNull()
  })

  it('allows either language to remain empty and counts Unicode characters', () => {
    const contact = normalizeCommunityContact()
    contact.group_name['zh-CN'] = '😀'.repeat(120)
    contact.description['zh-CN'] = '字'.repeat(1000)
    expect(validateCommunityContact(contact)).toBeNull()
    contact.group_name['en-US'] = 'x'.repeat(121)
    expect(validateCommunityContact(contact)?.field).toBe('group_name.en-US')
    contact.group_name['en-US'] = ''
    contact.description['en-US'] = '😀'.repeat(1001)
    expect(validateCommunityContact(contact)?.field).toBe('description.en-US')
  })

  it.each(['123 456', '１２３', '123abc', '1'.repeat(33)])('rejects invalid group numbers: %s', (value) => {
    const contact = normalizeCommunityContact()
    contact.group_number = value
    expect(validateCommunityContact(contact)?.field).toBe('group_number')
  })

  it.each([
    'javascript:alert(1)', 'data:image/png;base64,AA', '//example.com/join',
    '/join', 'https:///example.com', 'https://user:pass@example.com/join',
    'https://@example.com/join', 'https://example.com/\\join', 'https://example.com/jo\nin',
    'https://example.com/join%zz', 'https://example.com/%2', 'https://example.com/join?code=x#%q0',
  ])('rejects unsafe invitation URLs: %s', (value) => {
    const contact = normalizeCommunityContact()
    contact.invite_url = value
    expect(validateCommunityContact(contact)?.field).toBe('invite_url')
  })

  it.each([
    '//example.com/qr.png', '/\\example.com/qr.png', '/qr\u0000.png', 'data:image/png;base64,AA',
    '/qr%zz.png', '/qr.png#%', 'https://example.com/qr.png#%zz',
  ])('rejects unsafe QR image URLs: %s', (value) => {
    const contact = normalizeCommunityContact()
    contact.qr_image_url = value
    expect(validateCommunityContact(contact)?.field).toBe('qr_image_url')
  })

  it('accepts valid path and fragment escapes while preserving raw query values', () => {
    const contact = normalizeCommunityContact({
      invite_url: 'https://example.com/join%20us?code=%zz#step%201',
      qr_image_url: '/qr%20code.png?name=%zz#image%202',
    })
    expect(validateCommunityContact(contact)).toBeNull()
  })

  it('accepts root QR paths and preserves trimmed HTTP(S) links', () => {
    const contact = normalizeCommunityContact({
      group_number: `  ${'1'.repeat(32)}  `,
      invite_url: '  HTTPS://Example.com/join?code=AbC  ',
      qr_image_url: '/images/community-qr.png',
    })
    expect(validateCommunityContact(contact)).toBeNull()
    expect(contact.invite_url).toBe('HTTPS://Example.com/join?code=AbC')
    contact.qr_image_url = 'http://example.com/qr.png'
    expect(validateCommunityContact(contact)).toBeNull()
    contact.invite_url = `https://example.com/${'字'.repeat(2028)}`
    expect(validateCommunityContact(contact)).toBeNull()
    contact.invite_url += '字字'
    expect(validateCommunityContact(contact)).toEqual({ field: 'invite_url', message: 'urlTooLong' })
  })
})
