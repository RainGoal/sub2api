import { marked, Renderer } from 'marked'
import DOMPurify from 'dompurify'

export const DOC_MAX_IMAGE_BYTES = 2 * 1024 * 1024
export const DOC_MAX_CONTENT_BYTES = 12 * 1024 * 1024
export const DOC_IMAGE_TYPES = ['image/png', 'image/jpeg', 'image/webp']

export function documentByteLength(value: string): number {
  return new TextEncoder().encode(value).byteLength
}

export function documentImageToDataURL(file: File): Promise<string> {
  if (!DOC_IMAGE_TYPES.includes(file.type)) return Promise.reject(new Error('imageType'))
  if (file.size > DOC_MAX_IMAGE_BYTES) return Promise.reject(new Error('imageSize'))
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => typeof reader.result === 'string' ? resolve(reader.result) : reject(new Error('imageRead'))
    reader.onerror = () => reject(new Error('imageRead'))
    reader.readAsDataURL(file)
  })
}

/** The preview accepts Markdown, not executable HTML or embedded applications. */
export function renderDocsMarkdown(markdown: string): string {
  const renderer = new Renderer()
  renderer.html = ({ text }) => text.replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]!)
  renderer.checkbox = ({ checked }) => checked ? '[x] ' : '[ ] '
  const html = marked.parse(markdown, { async: false, gfm: true, breaks: false, renderer })
  const sanitized = DOMPurify.sanitize(html, {
    ALLOWED_TAGS: ['h1', 'h2', 'h3', 'h4', 'h5', 'h6', 'p', 'br', 'a', 'ul', 'ol', 'li', 'blockquote', 'pre', 'code', 'table', 'thead', 'tbody', 'tr', 'th', 'td', 'img', 'hr', 'strong', 'em', 'del'],
    ALLOWED_ATTR: ['href', 'src', 'alt', 'title', 'class', 'start', 'align'],
    ALLOW_DATA_ATTR: false,
    ALLOW_ARIA_ATTR: false
  })
  const doc = new DOMParser().parseFromString(sanitized, 'text/html')
  doc.querySelectorAll('img').forEach(image => {
    const src = image.getAttribute('src') || ''
    if (!/^(https?:\/\/|\/(?!\/)|data:image\/(?:png|jpeg|webp);base64,[a-z\d+/=]+$)/i.test(src)) {
      image.remove()
      return
    }
    image.setAttribute('loading', 'lazy')
    image.setAttribute('decoding', 'async')
  })
  doc.querySelectorAll('a[href]').forEach(link => {
    const href = link.getAttribute('href') || ''
    if (/^https?:\/\//i.test(href)) {
      link.setAttribute('target', '_blank')
      link.setAttribute('rel', 'noopener noreferrer')
    } else if (!/^(#|\/(?!\/)|mailto:)/i.test(href)) {
      link.removeAttribute('href')
    }
  })
  return doc.body.innerHTML
}

export function importedMarkdown(source: string, fallbackTitle: string): { title: string; content: string } {
  const normalized = source.replace(/^\uFEFF/, '').replace(/\r\n/g, '\n')
  const heading = normalized.match(/^#\s+(.+)\n/)
  return heading
    ? { title: heading[1].trim(), content: normalized.slice(heading[0].length).trimStart() }
    : { title: fallbackTitle, content: normalized }
}
