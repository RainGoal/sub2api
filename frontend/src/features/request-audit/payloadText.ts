export function formatPayloadJson(text: string): string | null {
  try {
    JSON.parse(text)
  } catch {
    return null
  }

  // Only change whitespace outside JSON tokens. Re-serializing would alter
  // duplicate keys, large numbers, and the original spelling of escapes.
  const tokens = text.match(/"(?:\\.|[^"\\])*"|[{}\[\],:]|[^"{}\[\],:\s]+/g)
  if (!tokens) return null
  const formatted: string[] = []
  let indent = 0
  const line = () => `\n${' '.repeat(indent)}`

  for (const [index, token] of tokens.entries()) {
    const previous = tokens[index - 1]
    if (token === '}' || token === ']') {
      indent -= 2
      if (previous !== '{' && previous !== '[') formatted.push(line())
    } else if (previous === '{' || previous === '[' || previous === ',') {
      formatted.push(line())
    } else if (previous === ':') {
      formatted.push(' ')
    }
    formatted.push(token)
    if (token === '{' || token === '[') indent += 2
    if (indent > 128) return null
  }

  return formatted.join('')
}

export function upstreamPayloadFile(bytes: ArrayBuffer, partial: boolean) {
  let text: string
  try {
    text = new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    return { blob: new Blob([bytes], { type: 'application/octet-stream' }), extension: 'bin', partial }
  }
  // A valid UTF-8 sequence can still be binary, for example a NUL-filled block.
  // eslint-disable-next-line no-control-regex
  if (/[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]/.test(text)) {
    return { blob: new Blob([bytes], { type: 'application/octet-stream' }), extension: 'bin', partial }
  }
  const formatted = partial ? null : formatPayloadJson(text)
  if (formatted !== null) {
    return { blob: new Blob([formatted + '\n'], { type: 'application/json' }), extension: 'json', partial }
  }
  return { blob: new Blob([bytes], { type: 'text/plain;charset=utf-8' }), extension: 'txt', partial }
}
