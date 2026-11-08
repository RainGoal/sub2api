import { describe, expect, it } from 'vitest'
import { DOC_MAX_IMAGE_BYTES, documentByteLength, documentImageToDataURL, importedMarkdown, renderDocsMarkdown } from '../docsMarkdown'

describe('documentation Markdown', () => {
  it('shows authored HTML as text, matching the public reader', () => {
    const element = document.createElement('div')
    element.innerHTML = renderDocsMarkdown('<strong>HTML</strong>\n\n<h2 id="spoofed">Title</h2>')
    expect(element.querySelector('strong, h2')).toBeNull()
    expect(element.textContent).toContain('<strong>HTML</strong>')
    expect(element.textContent).toContain('<h2 id="spoofed">Title</h2>')
  })

  it('renders tables, code, and Base64 screenshots without allowing active content', () => {
    const html = renderDocsMarkdown('| A | B |\n| --- | --- |\n| 1 | 2 |\n\n![Example](data:image/png;base64,aGVsbG8=)\n\n<script>alert(1)</script><img src="x" onerror="alert(1)"><iframe src="https://example.com"></iframe>')
    const element = document.createElement('div')
    element.innerHTML = html
    expect(element.querySelector('table')).not.toBeNull()
    expect(element.querySelector('img')?.getAttribute('src')).toBe('data:image/png;base64,aGVsbG8=')
    expect(element.querySelectorAll('img')).toHaveLength(1)
    expect(element.querySelector('script, iframe, [onerror]')).toBeNull()
  })

  it('keeps chapter links local and restricts unsafe images and links', () => {
    const html = renderDocsMarkdown('[Chapter](#install) [Docs](/docs/start) [External](https://example.com) [Bad](javascript:alert(1))\n\n<img src="data:image/svg+xml;base64,PHN2Zz4=" /><img src="//example.com/x.png" />')
    const element = document.createElement('div')
    element.innerHTML = html
    const links = element.querySelectorAll('a')
    expect(links[0].getAttribute('href')).toBe('#install')
    expect(links[0].hasAttribute('target')).toBe(false)
    expect(links[1].getAttribute('href')).toBe('/docs/start')
    expect(links[2].getAttribute('rel')).toBe('noopener noreferrer')
    expect(links[3].hasAttribute('href')).toBe(false)
    expect(element.querySelector('img')).toBeNull()
  })

  it('rejects unsupported and oversized screenshot files before reading', async () => {
    await expect(documentImageToDataURL(new File(['test'], 'bad.svg', { type: 'image/svg+xml' }))).rejects.toThrow('imageType')
    await expect(documentImageToDataURL(new File([new Uint8Array(DOC_MAX_IMAGE_BYTES + 1)], 'large.png', { type: 'image/png' }))).rejects.toThrow('imageSize')
    await expect(documentImageToDataURL(new File(['image'], 'ok.png', { type: 'image/png' }))).resolves.toMatch(/^data:image\/png;base64,/)
  })

  it('imports the first H1 as the title and measures UTF-8 storage size', () => {
    expect(importedMarkdown('\uFEFF# Quick start\r\n\r\n## Install\r\nText', 'Fallback')).toEqual({ title: 'Quick start', content: '## Install\nText' })
    expect(importedMarkdown('## Install\nText', 'Existing')).toEqual({ title: 'Existing', content: '## Install\nText' })
    expect(documentByteLength('中文')).toBe(6)
  })
})
