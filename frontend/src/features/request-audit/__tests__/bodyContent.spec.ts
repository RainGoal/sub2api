import { describe, expect, it } from 'vitest'
import { deflateSync } from 'node:zlib'
import { BODY_CONTENT_MAX_BYTES, parseBodyContent } from '../bodyContent'

const png = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aM9sAAAAASUVORK5CYII='
const pngURL = `data:image/png;base64,${png}`
const parse = (value: unknown) => parseBodyContent(JSON.stringify(value))
const withImage = (image: unknown) => parse({ messages: [{ role: 'user', content: [image] }] })
const imageBlock = (data: Buffer, mime = 'image/png') => ({ type: 'input_image', image_url: `data:${mime};base64,${data.toString('base64')}` })
function pngChunk(type: string, payload: Buffer) {
  const content = Buffer.concat([Buffer.from(type), payload]), header = Buffer.alloc(4), checksum = Buffer.alloc(4)
  let crc = 0xffffffff
  for (const value of content) {
    crc ^= value
    for (let bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ (0xedb88320 & -(crc & 1))
  }
  header.writeUInt32BE(payload.length); checksum.writeUInt32BE((crc ^ 0xffffffff) >>> 0)
  return Buffer.concat([header, content, checksum])
}
function pngImage(width: number, height: number, padding = 0) {
  const header = Buffer.alloc(13)
  header.writeUInt32BE(width, 0); header.writeUInt32BE(height, 4); header[8] = 1
  return Buffer.concat([
    Buffer.from('89504e470d0a1a0a', 'hex'), pngChunk('IHDR', header),
    ...(padding ? [pngChunk('tEXt', Buffer.concat([Buffer.from('note\0'), Buffer.alloc(padding - 5, 65)]))] : []),
    pngChunk('IDAT', deflateSync(Buffer.alloc((Math.ceil(width / 8) + 1) * height))), pngChunk('IEND', Buffer.alloc(0))
  ])
}
// Valid 2x3 images encoded by Pillow; exercise real container layouts, not signatures alone.
const images = {
  jpeg: '/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wAARCAADAAIDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAb/xAAUEAEAAAAAAAAAAAAAAAAAAAAA/8QAFQEBAQAAAAAAAAAAAAAAAAAABgf/xAAUEQEAAAAAAAAAAAAAAAAAAAAA/9oADAMBAAIRAxEAPwCLAGVxf//Z',
  progressive: '/9j/4AAQSkZJRgABAQAAAQABAAD/2wBDAAgGBgcGBQgHBwcJCQgKDBQNDAsLDBkSEw8UHRofHh0aHBwgJC4nICIsIxwcKDcpLDAxNDQ0Hyc5PTgyPC4zNDL/2wBDAQkJCQwLDBgNDRgyIRwhMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjIyMjL/wgARCAADAAIDASIAAhEBAxEB/8QAFQABAQAAAAAAAAAAAAAAAAAAAAX/xAAVAQEBAAAAAAAAAAAAAAAAAAAFBv/aAAwDAQACEAMQAAABigy4/8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABBQJ//8QAFBEBAAAAAAAAAAAAAAAAAAAAAP/aAAgBAwEBPwF//8QAFBEBAAAAAAAAAAAAAAAAAAAAAP/aAAgBAgEBPwF//8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQAGPwJ//8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABPyF//9oADAMBAAIAAwAAABAH/8QAFBEBAAAAAAAAAAAAAAAAAAAAAP/aAAgBAwEBPxB//8QAFBEBAAAAAAAAAAAAAAAAAAAAAP/aAAgBAgEBPxB//8QAFBABAAAAAAAAAAAAAAAAAAAAAP/aAAgBAQABPxB//9k=',
  gif: 'R0lGODdhAgADAIEAAP8AAAAAAAAAAAAAACwAAAAAAgADAAAIBgABCBwYEAA7',
  webp: 'UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoCAAMAAUAmJaACdLoB+AADsAD+8ut//NgVzXPv9//S4P0uD9Lg/9KQAAA=',
  webpLossless: 'UklGRhwAAABXRUJQVlA4TA8AAAAvAYAAAAcQ/Y/+ByKi/wEA',
  webpExtended: 'UklGRl4AAABXRUJQVlA4WAoAAAAQAAAAAQAAAgAAQUxQSAcAAAAAf39/f39/AFZQOCAwAAAA0AEAnQEqAgADAAFAJiWgAnS6AfgAA7AA/vLrf/zYFc1z7/f/0uD9Lg/S4P/SkAAA',
  webpAnimated: 'UklGRoQAAABXRUJQVlA4WAoAAAACAAAAAQAAAgAAQU5JTQYAAAAAAAAAAABBTk1GKAAAAAAAAAAAAAEAAAIAAGQAAAJWUDhMDwAAAC8BgAAABxD9j/4HIqL/AQBBTk1GKAAAAAAAAAAAAAEAAAIAAGQAAABWUDhMDwAAAC8BgAAABxDR//4HIqL/AQA=',
  apng: 'iVBORw0KGgoAAAANSUhEUgAAAAIAAAADCAIAAAA2iEnWAAAACGFjVEwAAAACAAAAAPONk3AAAAAaZmNUTAAAAAAAAAACAAAAAwAAAAAAAAAAAAEACgAAUa8H6AAAABFJREFUeJxj/M8AAkxgEkYBABRAAQVQ1S44AAAAGmZjVEwAAAABAAAAAgAAAAMAAAAAAAAAAAABAAoAAMrc7TwAAAAYZmRBVAAAAAJ4nGNkYPjPwMDAxAAGUAoAEkIBBVsNz4sAAAAASUVORK5CYII='
}

describe('parseBodyContent', () => {
  it('preserves OpenAI message order and plain text, including markup', () => {
    const result = parse({ messages: [
      { role: 'system', content: 'Rules' },
      { role: 'user', content: [{ type: 'text', text: '<img src=x onerror=alert(1)>' }, { type: 'image_url', image_url: { url: pngURL } }] },
      { role: 'assistant', content: 'Reply' }
    ] })
    expect(result.messages.map(message => message.role)).toEqual(['system', 'user', 'assistant'])
    expect(result.messages[1]?.blocks).toEqual([
      { kind: 'text', text: '<img src=x onerror=alert(1)>' },
      { kind: 'image', src: pngURL, mime: 'image/png', bytes: atob(png).length }
    ])
    expect(result).toMatchObject({ limited: false, unsupported: false })
  })

  it('keeps Anthropic system instructions and base64 image sources', () => {
    const result = parse({ system: [{ type: 'text', text: 'Be concise' }], messages: [
      { role: 'user', content: [{ type: 'image', source: { type: 'base64', media_type: 'image/png', data: png } }] }
    ] })
    expect(result.messages[0]).toEqual({ role: 'system', blocks: [{ kind: 'text', text: 'Be concise' }] })
    expect(result.messages[1]?.blocks[0]).toMatchObject({ kind: 'image', src: pngURL })
  })

  it.each([
    ['systemInstruction', 'inlineData', 'mimeType'],
    ['system_instruction', 'inline_data', 'mime_type']
  ])('supports Gemini %s and %s', (systemKey, imageKey, mimeKey) => {
    const result = parse({ [systemKey]: { parts: [{ text: 'System rules' }] }, contents: [
      { role: 'user', parts: [{ text: 'Describe' }, { [imageKey]: { [mimeKey]: 'image/png', data: png } }] },
      { role: 'model', parts: [{ text: 'A pixel' }] }
    ] })
    expect(result.messages.map(message => message.role)).toEqual(['system', 'user', 'model'])
    expect(result.messages[0]?.blocks).toEqual([{ kind: 'text', text: 'System rules' }])
    expect(result.messages[1]?.blocks[1]).toMatchObject({ kind: 'image', src: pngURL })
  })

  it('supports Responses input strings, messages and instructions', () => {
    expect(parse({ instructions: 'Be helpful', input: 'Hello' }).messages).toEqual([
      { role: 'system', blocks: [{ kind: 'text', text: 'Be helpful' }] },
      { role: 'user', blocks: [{ kind: 'text', text: 'Hello' }] }
    ])
    const result = parse({ input: [{ type: 'message', role: 'user', content: [
      { type: 'input_text', text: 'Describe' }, { type: 'input_image', image_url: pngURL }
    ] }] })
    expect(result.messages[0]?.blocks[1]).toMatchObject({ kind: 'image', src: pngURL })
  })

  it.each([
    { type: 'image_url', image_url: { url: 'https://private.example/image.png?token=secret' } },
    { type: 'input_image', image_url: '//private.example/image.png' },
    { type: 'image', source: { type: 'url', url: 'http://localhost/image.png' } },
    { fileData: { mimeType: 'image/png', fileUri: 'gs://private/image.png' } }
  ])('never includes an external image URL in display data', image => {
    const result = image.fileData
      ? parse({ contents: [{ parts: [image] }] })
      : withImage(image)
    expect(result.messages[0]?.blocks).toEqual([{ kind: 'image-unavailable', reason: 'external' }])
    expect(JSON.stringify(result)).not.toMatch(/private|localhost|secret|gs:\/\//)
  })

  it.each([
    ['data:image/svg+xml;base64,PHN2Zz48L3N2Zz4=', 'unsupported'],
    ['data:text/html;base64,PHN2Zz48L3N2Zz4=', 'unsupported'],
    ['data:image/png,not-base64', 'invalid'],
    [`data:image/png;base64,${png}\n`, 'invalid'],
    ['data:image/png;base64,AAAA!!!!', 'invalid'],
    ['data:image/png;base64,iVBORw0KGgp=', 'invalid'],
    ['data:image/png;base64,iVBORw0KGgo', 'invalid'],
    [`data:image/jpeg;base64,${png}`, 'invalid'],
    ['data:image/png;base64,', 'invalid']
  ])('rejects unsupported or malformed inline image %s', (url, reason) => {
    expect(withImage({ type: 'image_url', image_url: { url } }).messages[0]?.blocks)
      .toEqual([{ kind: 'image-unavailable', reason }])
  })

  it.each([
    ['image/png', '\x89PNG\r\n\x1a\n'],
    ['image/jpeg', '\xff\xd8\xff\xd9'],
    ['image/gif', 'GIF89a'],
    ['image/gif', 'GIF87a'],
    ['image/webp', 'RIFF\x04\x00\x00\x00WEBP']
  ])('rejects signatures without trustworthy dimensions for %s', (mime, signature) => {
    const src = `data:${mime};base64,${btoa(signature)}`
    expect(withImage({ type: 'input_image', image_url: src }).messages[0]?.blocks[0])
      .toEqual({ kind: 'image-unavailable', reason: 'invalid' })
  })

  it('limits each decoded image to 2 MiB before decoding', () => {
    const bytes = pngImage(1, 1, 2 * 1024 * 1024 - pngImage(1, 1).length - 12)
    expect(bytes.length).toBe(2 * 1024 * 1024)
    expect(withImage(imageBlock(bytes)).messages[0]?.blocks[0]).toMatchObject({ kind: 'image', bytes: 2 * 1024 * 1024 })
    expect(withImage(imageBlock(Buffer.concat([bytes, Buffer.from([0])])))).toMatchObject({ limited: true, messages: [
      { blocks: [{ kind: 'image-unavailable', reason: 'tooLarge' }] }
    ] })
  })

  it.each([
    ['jpeg', 'image/jpeg'], ['progressive', 'image/jpeg'], ['gif', 'image/gif'],
    ['webp', 'image/webp'], ['webpLossless', 'image/webp'], ['webpExtended', 'image/webp'],
    ['webpAnimated', 'image/webp'], ['apng', 'image/png']
  ] as const)('preserves valid %s previews', (name, mime) => {
    expect(withImage(imageBlock(Buffer.from(images[name], 'base64'), mime)).messages[0]?.blocks[0]).toMatchObject({ kind: 'image' })
  })

  it('bounds dimensions and pixels before compressed PNGs become image sources', () => {
    for (const [width, height] of [[8192, 1], [4096, 4096]]) {
      expect(withImage(imageBlock(pngImage(width!, height!))).messages[0]?.blocks[0]).toMatchObject({ kind: 'image' })
    }
    for (const [width, height] of [[8193, 1], [4097, 4096], [12000, 12000]]) {
      const data = pngImage(width!, height!)
      expect(data.length).toBeLessThan(2 * 1024 * 1024)
      expect(withImage(imageBlock(data)).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'tooLarge' })
    }
  })

  it('limits cumulative image pixels across messages and retains later text', () => {
    const image = imageBlock(pngImage(4096, 4096))
    const result = parse({ messages: [{ role: 'user', content: [image, image] }, { role: 'user', content: [image, { type: 'text', text: 'Still visible' }] }] })
    expect(result.messages[0]?.blocks.every(block => block.kind === 'image')).toBe(true)
    expect(result.messages[1]?.blocks).toEqual([{ kind: 'image-unavailable', reason: 'tooLarge' }, { kind: 'text', text: 'Still visible' }])
    expect(result.limited).toBe(true)
  })

  it('rejects oversized JPEG SOF dimensions and zero-height DNL images', () => {
    const jpeg = Buffer.from(images.jpeg, 'base64'), start = jpeg.indexOf(Buffer.from([0xff, 0xc0]))
    jpeg.writeUInt16BE(8193, start + 7)
    expect(withImage(imageBlock(jpeg, 'image/jpeg')).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'tooLarge' })
    jpeg.writeUInt16BE(2, start + 7); jpeg.writeUInt16BE(0, start + 5)
    expect(withImage(imageBlock(jpeg, 'image/jpeg')).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'invalid' })
  })

  it('bounds GIF frame rectangles and includes animation canvases in the pixel budget', () => {
    const gif = Buffer.from(images.gif, 'base64'), start = gif.indexOf(Buffer.from('2c00000000', 'hex'))
    const animated = Buffer.concat([gif.subarray(0, -1), gif.subarray(start)])
    expect(withImage(imageBlock(animated, 'image/gif')).messages[0]?.blocks[0]).toMatchObject({ kind: 'image' })
    animated.writeUInt16LE(4096, 6); animated.writeUInt16LE(4096, 8)
    expect(withImage(imageBlock(animated, 'image/gif')).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'tooLarge' })
    gif.writeUInt16LE(3, start + 5)
    expect(withImage(imageBlock(gif, 'image/gif')).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'invalid' })
  })

  it('checks APNG frame rectangles and counts all animation canvases', () => {
    const image = Buffer.from(images.apng, 'base64'), firstFrame = image.indexOf('fcTL') + 4
    image.writeUInt32BE(3, firstFrame + 4)
    expect(withImage(imageBlock(image)).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'invalid' })
    image.writeUInt32BE(2, firstFrame + 4)
    image.writeUInt32BE(4096, 16); image.writeUInt32BE(4096, 20)
    expect(withImage(imageBlock(image)).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'tooLarge' })
  })

  it.each([0, 1, 0xffffffff])('rejects inconsistent APNG declared frame count %s', frames => {
    const image = Buffer.from(images.apng, 'base64'), start = image.indexOf('acTL') - 4, control = Buffer.alloc(8)
    control.writeUInt32BE(frames)
    const changed = Buffer.concat([image.subarray(0, start), pngChunk('acTL', control), image.subarray(start + 20)])
    expect(withImage(imageBlock(changed)).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'invalid' })
  })

  it('checks WebP bitstream dimensions, container consistency and animation pixel budgets', () => {
    const lossy = Buffer.from(images.webp, 'base64')
    lossy.writeUInt16LE(8193, 26)
    expect(withImage(imageBlock(lossy, 'image/webp')).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'tooLarge' })
    const lossless = Buffer.from(images.webpLossless, 'base64')
    lossless.writeUInt32LE(8192 + (2 << 14), 21)
    expect(withImage(imageBlock(lossless, 'image/webp')).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'tooLarge' })
    const extended = Buffer.from(images.webpExtended, 'base64')
    extended.writeUIntLE(2, 24, 3)
    expect(withImage(imageBlock(extended, 'image/webp')).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'invalid' })
    const animated = Buffer.from(images.webpAnimated, 'base64'), frame = animated.indexOf('ANMF') + 8
    animated.writeUIntLE(2, frame + 6, 3)
    expect(withImage(imageBlock(animated, 'image/webp')).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'invalid' })
    animated.writeUIntLE(1, frame + 6, 3)
    animated.writeUIntLE(4095, 24, 3); animated.writeUIntLE(4095, 27, 3)
    expect(withImage(imageBlock(animated, 'image/webp')).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'tooLarge' })
    animated.writeUInt32LE(animated.length + 1, 4)
    expect(withImage(imageBlock(animated, 'image/webp')).messages[0]?.blocks[0]).toEqual({ kind: 'image-unavailable', reason: 'invalid' })
  })

  it('marks tool and unknown blocks without recursively interpreting their contents', () => {
    const result = parse({ messages: [{ role: 'assistant', content: [
      { type: 'tool_use', input: { content: [{ type: 'text', text: 'nested secret' }] } },
      { type: 'tool_result', content: [{ type: 'image_url', image_url: { url: pngURL } }] },
      { type: 'future_block', content: [{ type: 'text', text: 'nested secret' }] }
    ] }, { role: 'assistant', content: null, tool_calls: [{ id: 'call' }] }] })
    expect(result.unsupported).toBe(true)
    expect(result.messages[0]?.blocks).toEqual([
      { kind: 'unsupported', label: 'tool_use' },
      { kind: 'unsupported', label: 'tool_result' },
      { kind: 'unsupported', label: 'future_block' }
    ])
    expect(result.messages[1]?.blocks).toEqual([{ kind: 'unsupported', label: 'tool_calls' }])
    expect(JSON.stringify(result)).not.toContain('nested secret')
    expect(JSON.stringify(result)).not.toContain(png)
  })

  it.each(['{broken', 'null', '[]', '"hello"', '{"model":"demo"}', '{"messages":{}}', '{"messages":[]}'])('gracefully rejects unsupported input %s', content => {
    expect(parseBodyContent(content)).toEqual({ messages: [], limited: false, unsupported: true })
  })

  it('treats malformed Responses item types as unknown data without coercing them', () => {
    expect(parse({ input: [{ type: { toString: null }, content: 'nested' }] }).messages)
      .toEqual([{ role: 'tool', blocks: [{ kind: 'unsupported', label: 'unknown' }] }])
  })

  it('enforces the body limit in UTF-8 bytes, including multibyte text', () => {
    const prefix = '{"input":"', suffix = '"}'
    const remaining = BODY_CONTENT_MAX_BYTES - prefix.length - suffix.length
    const content = prefix + '中'.repeat(Math.floor(remaining / 3)) + 'a'.repeat(remaining % 3) + suffix
    expect(new TextEncoder().encode(content).byteLength).toBe(BODY_CONTENT_MAX_BYTES)
    expect(parseBodyContent(content).limited).toBe(false)
    expect(parseBodyContent(content + ' ')).toEqual({ messages: [], limited: true, unsupported: true })
    expect(parseBodyContent(' '.repeat(BODY_CONTENT_MAX_BYTES + 1))).toEqual({ messages: [], limited: true, unsupported: true })
  })

  it('bounds message counts and both per-message and total block counts', () => {
    const message = { role: 'user', content: 'hello' }
    expect(parse({ messages: Array(200).fill(message) }).limited).toBe(false)
    const manyMessages = parse({ messages: Array(201).fill(message) })
    expect(manyMessages.messages).toHaveLength(200)
    expect(manyMessages.limited).toBe(true)
    const blocks = Array(101).fill({ type: 'text', text: 'hello' })
    const manyBlocks = parse({ messages: [{ role: 'user', content: blocks }] })
    expect(manyBlocks.messages[0]?.blocks).toHaveLength(100)
    expect(manyBlocks.limited).toBe(true)
    const manyTotal = parse({ messages: Array(11).fill({ role: 'user', content: blocks.slice(0, 100) }) })
    expect(manyTotal.messages.reduce((total, value) => total + value.blocks.length, 0)).toBe(1000)
    expect(manyTotal.limited).toBe(true)
  })
})
