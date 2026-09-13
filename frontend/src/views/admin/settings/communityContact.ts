import type { CommunityContact } from '@/types'

type CommunityLocale = keyof CommunityContact['group_name']
export type CommunityContactField =
  | `group_name.${CommunityLocale}`
  | `description.${CommunityLocale}`
  | 'group_number'
  | 'invite_url'
  | 'qr_image_url'

export interface CommunityContactValidationError {
  field: CommunityContactField
  message: 'groupNameTooLong' | 'descriptionTooLong' | 'groupNumberInvalid'
    | 'inviteUrlInvalid' | 'qrImageUrlInvalid' | 'urlTooLong'
}

function asRecord(value: unknown): Record<string, unknown> {
  return value !== null && typeof value === 'object' ? value as Record<string, unknown> : {}
}

function asText(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

export function normalizeCommunityContact(value?: unknown): CommunityContact {
  const contact = asRecord(value)
  const name = asRecord(contact.group_name)
  const description = asRecord(contact.description)
  return {
    group_name: { 'zh-CN': asText(name['zh-CN']), 'en-US': asText(name['en-US']) },
    description: { 'zh-CN': asText(description['zh-CN']), 'en-US': asText(description['en-US']) },
    group_number: asText(contact.group_number),
    invite_url: asText(contact.invite_url),
    qr_image_url: asText(contact.qr_image_url),
  }
}

function isCommunityUrlAllowed(value: string, allowRootPath: boolean): boolean {
  for (const character of value) {
    const code = character.charCodeAt(0)
    if (character === '\\' || code <= 0x1f || (code >= 0x7f && code <= 0x9f)) return false
  }
  // Match net/url.Parse: path and fragment escapes must be valid; the query stays raw.
  const [beforeFragment = '', ...fragmentParts] = value.split('#')
  const pathAndAuthority = beforeFragment.split('?', 1)[0] ?? ''
  if ([pathAndAuthority, fragmentParts.join('#')].some((part) => /%(?![\da-f]{2})/i.test(part))) return false
  if (allowRootPath && value.startsWith('/')) return !value.startsWith('//')
  const authority = value.match(/^https?:\/\/([^/?#]*)/i)?.[1]
  if (!authority || authority.includes('@')) return false
  try {
    const parsed = new URL(value)
    return (parsed.protocol === 'http:' || parsed.protocol === 'https:')
      && !!parsed.hostname && !parsed.username && !parsed.password
  } catch {
    return false
  }
}

export function validateCommunityContact(value: CommunityContact): CommunityContactValidationError | null {
  const contact = normalizeCommunityContact(value)
  for (const locale of ['zh-CN', 'en-US'] as const) {
    if (Array.from(contact.group_name[locale]).length > 120) {
      return { field: `group_name.${locale}`, message: 'groupNameTooLong' }
    }
    if (Array.from(contact.description[locale]).length > 1000) {
      return { field: `description.${locale}`, message: 'descriptionTooLong' }
    }
  }
  if (contact.group_number && !/^[0-9]{1,32}$/.test(contact.group_number)) {
    return { field: 'group_number', message: 'groupNumberInvalid' }
  }
  for (const field of ['invite_url', 'qr_image_url'] as const) {
    if (Array.from(contact[field]).length > 2048) return { field, message: 'urlTooLong' }
    if (contact[field] && !isCommunityUrlAllowed(contact[field], field === 'qr_image_url')) {
      return { field, message: field === 'invite_url' ? 'inviteUrlInvalid' : 'qrImageUrlInvalid' }
    }
  }
  return null
}
