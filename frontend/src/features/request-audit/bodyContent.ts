export const BODY_CONTENT_MAX_BYTES = 4 * 1024 * 1024
const IMAGE_MAX_BYTES = 2 * 1024 * 1024
const IMAGE_MAX_DIMENSION = 8192
const IMAGE_MAX_PIXELS = 16 * 1024 * 1024
const BODY_IMAGE_MAX_PIXELS = 32 * 1024 * 1024
const MAX_MESSAGES = 200
const MAX_MESSAGE_BLOCKS = 100
const MAX_TOTAL_BLOCKS = 1000

export type BodyContentBlock =
  | { kind: 'text'; text: string }
  | { kind: 'image'; src: string; mime: string; bytes: number }
  | { kind: 'image-unavailable'; reason: 'external' | 'invalid' | 'unsupported' | 'tooLarge'; label?: string }
  | { kind: 'unsupported'; label: string }

export interface BodyContentResult {
  messages: Array<{ role: string; blocks: BodyContentBlock[] }>
  limited: boolean
  unsupported: boolean
}

type ObjectValue = Record<string, unknown>
const asObject = (value: unknown): ObjectValue | undefined =>
  value !== null && typeof value === 'object' && !Array.isArray(value) ? value as ObjectValue : undefined
const unavailable = (reason: Extract<BodyContentBlock, { kind: 'image-unavailable' }>['reason']): BodyContentBlock =>
  ({ kind: 'image-unavailable', reason })
const unsupported = (label: string): BodyContentBlock => ({ kind: 'unsupported', label: label.slice(0, 80) })

type ImageSize = { width: number; height: number; frames: number }
type ImageBudget = { pixels: number }
const uint = (data: string, offset: number, bytes: number, littleEndian = false) => {
  let value = 0
  for (let index = 0; index < bytes; index++) value = value * 256 + data.charCodeAt(offset + (littleEndian ? bytes - index - 1 : index))
  return value
}
const fits = (size: ImageSize, x: number, y: number, width: number, height: number) =>
  width > 0 && height > 0 && x + width <= size.width && y + height <= size.height

// Read container dimensions before assigning any image src. PNG/APNG: W3C PNG 3,
// sections 11.2.2 and 11.3.6.2 (IHDR and fcTL); do not inflate image data here.
function pngSize(data: string): ImageSize | null {
  if (data.length < 33 || uint(data, 8, 4) !== 13 || data.slice(12, 16) !== 'IHDR') return null
  const size = { width: uint(data, 16, 4), height: uint(data, 20, 4), frames: 0 }
  let imageData = false, separateDefault = false, declaredFrames = 0
  for (let offset = 8; offset + 12 <= data.length;) {
    const length = uint(data, offset, 4), type = data.slice(offset + 4, offset + 8), start = offset + 8
    if (start + length + 4 > data.length || (type === 'IHDR' && offset !== 8)) return null
    if (type === 'IDAT') imageData = true
    if (type === 'acTL') {
      if (imageData || declaredFrames || length !== 8) return null
      declaredFrames = uint(data, start, 4)
      if (!declaredFrames) return null
    }
    if (type === 'fcTL') {
      if (!declaredFrames || length !== 26 || !fits(size, uint(data, start + 12, 4), uint(data, start + 16, 4), uint(data, start + 4, 4), uint(data, start + 8, 4))) return null
      if (!size.frames && imageData) separateDefault = true
      size.frames++
    }
    if (type === 'IEND') return imageData && length === 0 && size.frames === declaredFrames ? { ...size, frames: Math.max(1, size.frames + Number(separateDefault)) } : null
    offset = start + length + 4
  }
  return null
}

// GIF89a sections 18, 20 and 22: validate every frame against the logical screen.
function gifSize(data: string): ImageSize | null {
  if (data.length < 13) return null
  const size = { width: uint(data, 6, 2, true), height: uint(data, 8, 2, true), frames: 0 }
  const tableBytes = (flags: number) => flags & 0x80 ? 3 * 2 ** ((flags & 7) + 1) : 0
  let offset = 13 + tableBytes(data.charCodeAt(10))
  const skipBlocks = () => {
    while (offset < data.length) {
      const length = data.charCodeAt(offset++)
      if (!length) return true
      offset += length
    }
    return false
  }
  while (offset < data.length) {
    const type = data.charCodeAt(offset)
    if (type === 0x3b) return size.frames ? size : null
    if (type === 0x21) { offset += 2; if (!skipBlocks()) return null; continue }
    if (type !== 0x2c || offset + 10 > data.length || !fits(size, uint(data, offset + 1, 2, true), uint(data, offset + 3, 2, true), uint(data, offset + 5, 2, true), uint(data, offset + 7, 2, true))) return null
    size.frames++
    offset += 10 + tableBytes(data.charCodeAt(offset + 9))
    if (offset >= data.length || data.charCodeAt(offset) < 2 || data.charCodeAt(offset++) > 8 || !skipBlocks()) return null
  }
  return null
}

// SOFn layout follows libjpeg-turbo src/jdmarker.c:get_sof; reject DNL dimensions.
function jpegSize(data: string): ImageSize | null {
  let size: ImageSize | null = null
  for (let offset = 2; offset < data.length;) {
    if (data.charCodeAt(offset++) !== 0xff) return null
    while (data.charCodeAt(offset) === 0xff) offset++
    const marker = data.charCodeAt(offset++)
    if (marker === 0xda) return size
    if (marker === 0xd9 || marker === 0 || marker === 0xd8) return null
    if (marker === 1 || (marker >= 0xd0 && marker <= 0xd7)) continue
    if (offset + 2 > data.length) return null
    const length = uint(data, offset, 2)
    if (length < 2 || offset + length > data.length) return null
    if ([0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf].includes(marker)) {
      if (size || length < 8 || length !== 8 + 3 * data.charCodeAt(offset + 7)) return null
      size = { width: uint(data, offset + 5, 2), height: uint(data, offset + 3, 2), frames: 1 }
    }
    offset += length
  }
  return null
}

// WebP RIFF container specification, VP8 RFC6386 section 9.1, VP8L image header.
function webpFrameSize(type: string, data: string): ImageSize | null {
  if (type === 'VP8 ' && data.length >= 10 && !(data.charCodeAt(0) & 1) && data.slice(3, 6) === '\x9d\x01\x2a') {
    return { width: uint(data, 6, 2, true) & 0x3fff, height: uint(data, 8, 2, true) & 0x3fff, frames: 1 }
  }
  if (type === 'VP8L' && data.length >= 5 && data.charCodeAt(0) === 0x2f && !(data.charCodeAt(4) & 0xe0)) {
    const bits = uint(data, 1, 4, true)
    return { width: (bits & 0x3fff) + 1, height: ((bits >>> 14) & 0x3fff) + 1, frames: 1 }
  }
  return null
}

function webpSize(data: string): ImageSize | null {
  if (data.length < 20 || uint(data, 4, 4, true) + 8 !== data.length) return null
  let canvas: ImageSize | null = null, image: ImageSize | null = null, animated = false, frames = 0
  const chunks = (start: number, end: number, visit: (type: string, body: string) => boolean) => {
    while (start + 8 <= end) {
      const length = uint(data, start + 4, 4, true), next = start + 8 + length + length % 2
      if (next > end || !visit(data.slice(start, start + 4), data.slice(start + 8, start + 8 + length))) return false
      start = next
    }
    return start === end
  }
  const valid = chunks(12, data.length, (type, body) => {
    if (type === 'VP8X') {
      if (canvas || image || body.length !== 10) return false
      canvas = { width: uint(body, 4, 3, true) + 1, height: uint(body, 7, 3, true) + 1, frames: 1 }
      animated = !!(body.charCodeAt(0) & 2)
    } else if (type === 'VP8 ' || type === 'VP8L') {
      if (image || animated) return false
      image = webpFrameSize(type, body)
      if (!image || (canvas && (image.width !== canvas.width || image.height !== canvas.height))) return false
    } else if (type === 'ANMF') {
      if (!canvas || !animated || body.length < 16) return false
      const width = uint(body, 6, 3, true) + 1, height = uint(body, 9, 3, true) + 1
      if (!fits(canvas, uint(body, 0, 3, true) * 2, uint(body, 3, 3, true) * 2, width, height)) return false
      let frame: ImageSize | null = null
      for (let offset = 16; offset + 8 <= body.length;) {
        const length = uint(body, offset + 4, 4, true), next = offset + 8 + length + length % 2, kind = body.slice(offset, offset + 4)
        if (next > body.length) return false
        if (kind === 'VP8 ' || kind === 'VP8L') {
          if (frame) return false
          frame = webpFrameSize(kind, body.slice(offset + 8, offset + 8 + length))
          if (!frame || frame.width !== width || frame.height !== height) return false
        }
        offset = next
        if (offset === body.length && frame) { frames++; return true }
      }
      return false
    }
    return true
  })
  const size = canvas as ImageSize | null
  return valid ? (animated && size && frames ? { ...size, frames } : image) : null
}

function inlineImage(mimeValue: unknown, data: unknown, budget: ImageBudget): BodyContentBlock {
  if (typeof mimeValue !== 'string' || typeof data !== 'string') return unavailable('invalid')
  const mime = mimeValue.toLowerCase()
  if (!['image/png', 'image/jpeg', 'image/webp', 'image/gif'].includes(mime)) return unavailable('unsupported')
  const padding = data.endsWith('==') ? 2 : data.endsWith('=') ? 1 : 0
  const bytes = data.length / 4 * 3 - padding
  if (bytes > IMAGE_MAX_BYTES) return unavailable('tooLarge')
  if (!data || data.length % 4 !== 0 || !/^[A-Za-z0-9+/]*={0,2}$/.test(data)) return unavailable('invalid')
  let binary: string
  try {
    binary = atob(data)
    // Reject noncanonical padding bits as well as malformed Base64 syntax.
    if (btoa(binary) !== data) return unavailable('invalid')
  } catch {
    return unavailable('invalid')
  }
  const valid = mime === 'image/png' ? binary.startsWith('\x89PNG\r\n\x1a\n')
    : mime === 'image/jpeg' ? binary.startsWith('\xff\xd8\xff')
      : mime === 'image/gif' ? binary.startsWith('GIF87a') || binary.startsWith('GIF89a')
        : binary.startsWith('RIFF') && binary.slice(8, 12) === 'WEBP'
  if (!valid) return unavailable('invalid')
  const size = mime === 'image/png' ? pngSize(binary) : mime === 'image/jpeg' ? jpegSize(binary)
    : mime === 'image/gif' ? gifSize(binary) : webpSize(binary)
  if (!size || !size.width || !size.height) return unavailable('invalid')
  // Count a complete canvas per animation frame, conservatively bounding frame caches.
  const pixels = size.width * size.height * size.frames
  if (size.width > IMAGE_MAX_DIMENSION || size.height > IMAGE_MAX_DIMENSION || pixels > IMAGE_MAX_PIXELS || budget.pixels + pixels > BODY_IMAGE_MAX_PIXELS) return unavailable('tooLarge')
  budget.pixels += pixels
  return { kind: 'image', src: `data:${mime};base64,${data}`, mime, bytes }
}

function imageURL(value: unknown, budget: ImageBudget): BodyContentBlock {
  const url = typeof value === 'string' ? value : asObject(value)?.url
  if (typeof url !== 'string' || !url) return unavailable('invalid')
  if (!/^data:/i.test(url)) return unavailable('external')
  const comma = url.indexOf(',')
  if (comma < 0) return unavailable('invalid')
  const header = url.slice(5, comma)
  const mime = header.split(';', 1)[0] ?? ''
  if (!['image/png', 'image/jpeg', 'image/webp', 'image/gif'].includes(mime.toLowerCase())) return unavailable('unsupported')
  if (header.toLowerCase() !== `${mime.toLowerCase()};base64`) return unavailable('invalid')
  return inlineImage(mime, url.slice(comma + 1), budget)
}

function contentBlock(value: unknown, gemini: boolean, budget: ImageBudget): BodyContentBlock {
  const block = asObject(value)
  if (!block) return unsupported('unknown')
  if (gemini) {
    if (typeof block.text === 'string') return { kind: 'text', text: block.text }
    const inline = asObject(block.inlineData ?? block.inline_data)
    if (inline) return inlineImage(inline.mimeType ?? inline.mime_type, inline.data, budget)
    const file = asObject(block.fileData ?? block.file_data)
    const mime = file?.mimeType ?? file?.mime_type
    if (typeof mime === 'string' && mime.startsWith('image/')) return unavailable('external')
    return unsupported(Object.keys(block)[0] ?? 'unknown')
  }
  switch (block.type) {
    case 'text':
    case 'input_text':
    case 'output_text':
      return typeof block.text === 'string' ? { kind: 'text', text: block.text } : unsupported(block.type)
    case 'image_url':
    case 'input_image':
      return block.file_id ? unavailable('unsupported') : imageURL(block.image_url, budget)
    case 'image': {
      const source = asObject(block.source)
      if (source?.type === 'base64') return inlineImage(source.media_type, source.data, budget)
      if (source?.type === 'url') return typeof source.url === 'string' ? unavailable('external') : unavailable('invalid')
      return unavailable('unsupported')
    }
    default:
      return unsupported(typeof block.type === 'string' ? block.type : 'unknown')
  }
}

export function parseBodyContent(content: string): BodyContentResult {
  const result: BodyContentResult = { messages: [], limited: false, unsupported: false }
  // Check the cheap UTF-16 bound first, before allocating a UTF-8 buffer or parsing JSON.
  if (content.length > BODY_CONTENT_MAX_BYTES || new TextEncoder().encode(content).byteLength > BODY_CONTENT_MAX_BYTES) {
    return { ...result, limited: true, unsupported: true }
  }
  let root: ObjectValue | undefined
  try {
    root = asObject(JSON.parse(content))
  } catch {
    return { ...result, unsupported: true }
  }
  if (!root) return { ...result, unsupported: true }
  let totalBlocks = 0
  const imageBudget: ImageBudget = { pixels: 0 }

  function addMessage(role: unknown, contentValue: unknown, gemini = false, extra?: string): boolean {
    if (result.messages.length >= MAX_MESSAGES || totalBlocks >= MAX_TOTAL_BLOCKS) {
      result.limited = true
      return false
    }
    const blocks: BodyContentBlock[] = []
    const values = Array.isArray(contentValue) ? contentValue : [contentValue]
    for (const value of values) {
      if ((value === null || value === undefined) && extra) continue
      if (blocks.length >= MAX_MESSAGE_BLOCKS || totalBlocks >= MAX_TOTAL_BLOCKS) {
        result.limited = true
        break
      }
      const block = typeof contentValue === 'string' ? { kind: 'text' as const, text: contentValue } : contentBlock(value, gemini, imageBudget)
      blocks.push(block)
      totalBlocks++
      if (block.kind === 'unsupported' || block.kind === 'image-unavailable') result.unsupported = true
      if (block.kind === 'image-unavailable' && block.reason === 'tooLarge') result.limited = true
    }
    if (extra) {
      result.unsupported = true
      if (blocks.length < MAX_MESSAGE_BLOCKS && totalBlocks < MAX_TOTAL_BLOCKS) {
        blocks.push(unsupported(extra))
        totalBlocks++
      } else result.limited = true
    }
    const name = typeof role === 'string' ? role : 'unknown'
    if (name.length > 64) result.limited = true
    result.messages.push({ role: name.slice(0, 64), blocks })
    return true
  }

  function addRecords(values: unknown[], gemini = false, responses = false) {
    for (const value of values) {
      const message = asObject(value)
      if (responses && message?.type !== undefined && message.type !== 'message') {
        const directContent = typeof message.type === 'string' && ['input_text', 'input_image', 'output_text'].includes(message.type)
        if (!addMessage(directContent ? 'user' : 'tool', [value])) break
        continue
      }
      const extra = Array.isArray(message?.tool_calls) && message.tool_calls.length ? 'tool_calls'
        : message?.function_call ? 'function_call' : undefined
      const body = gemini ? message?.parts : message?.content
      if (!addMessage(message?.role ?? (gemini || responses ? 'user' : 'unknown'), body, gemini, extra)) break
    }
  }

  if (Array.isArray(root.messages)) {
    if (root.system !== undefined && root.system !== null) addMessage('system', root.system)
    addRecords(root.messages)
  } else if (Array.isArray(root.contents)) {
    const system = root.systemInstruction ?? root.system_instruction
    if (system !== undefined && system !== null) addMessage('system', asObject(system)?.parts, true)
    addRecords(root.contents, true)
  } else if (typeof root.input === 'string' || Array.isArray(root.input)) {
    if (root.instructions !== undefined && root.instructions !== null) addMessage('system', root.instructions)
    if (typeof root.input === 'string') addMessage('user', root.input)
    else addRecords(root.input, false, true)
  } else result.unsupported = true
  if (result.messages.length === 0) result.unsupported = true
  return result
}
