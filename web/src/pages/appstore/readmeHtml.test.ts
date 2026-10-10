import { describe, expect, it } from 'vitest'
import { readmeToHtml } from './readmeHtml'

// The patterns are the ones Uptime Kuma's and Nginx Proxy Guard's READMEs
// use, written out small.
const BASE = 'https://raw.githubusercontent.com/owner/repo/main/'

describe('readmeToHtml', () => {
  it('resolves a relative image in raw HTML against the raw file host', () => {
    const html = readmeToHtml('<div align="center">\n  <img src="./public/icon.svg" width="128" alt="Logo" />\n</div>\n', BASE)
    expect(html).toContain('src="https://raw.githubusercontent.com/owner/repo/main/public/icon.svg"')
  })

  it('resolves a <picture> banner, srcset included', () => {
    const html = readmeToHtml('<picture>\n  <source media="(prefers-color-scheme: dark)" srcset="./banner_dark.png">\n  <img src="./banner.png" alt="Banner">\n</picture>\n', BASE)
    expect(html).toContain('srcset="https://raw.githubusercontent.com/owner/repo/main/banner_dark.png"')
    expect(html).toContain('src="https://raw.githubusercontent.com/owner/repo/main/banner.png"')
  })

  it('resolves every address in one tag', () => {
    const html = readmeToHtml('<img src="./a.png" srcset="./a@2x.png 2x, ./a@3x.png 3x" alt="a">\n', BASE)
    expect(html).toContain(`src="${BASE}a.png"`)
    expect(html).toContain(`srcset="${BASE}a@2x.png 2x, ${BASE}a@3x.png 3x"`)
  })

  it('renders a badge that links somewhere as an image, not as its markdown', () => {
    const html = readmeToHtml('[![Sponsors](https://img.shields.io/github/sponsors/x)](https://github.com/sponsors/x)\n', BASE)
    expect(html).toContain('<img src="https://img.shields.io/github/sponsors/x"')
    expect(html).not.toContain('![Sponsors]')
    expect(html).toContain('href="https://github.com/sponsors/x"')
  })

  it("opens a link to a repository file on GitHub's page for it", () => {
    const html = readmeToHtml('[License](LICENSE) and [한국어](./README_KO.md)\n', BASE)
    expect(html).toContain('href="https://github.com/owner/repo/blob/main/LICENSE"')
    expect(html).toContain('href="https://github.com/owner/repo/blob/main/README_KO.md"')
  })

  it('leaves in-page anchors as text: there is no heading to land on', () => {
    const md = readmeToHtml('[Quick start](#-quick-start)\n', BASE)
    expect(md).toContain('Quick start')
    expect(md).not.toContain('href="#')
    const raw = readmeToHtml('<a href="#-features">Features</a>\n', BASE)
    expect(raw).toContain('Features')
    expect(raw).not.toContain('href="#')
  })

  it('leaves absolute addresses alone', () => {
    const html = readmeToHtml('<img src="https://example.com/a.png">\n\n[mail](mailto:a@example.com)\n\n![x](data:image/png;base64,AAAA)\n', BASE)
    expect(html).toContain('src="https://example.com/a.png"')
    expect(html).toContain('href="mailto:a@example.com"')
    expect(html).toContain('src="data:image/png;base64,AAAA"')
  })

  it("opens a README's own links in a new tab", () => {
    const html = readmeToHtml('<a href="https://weblate.example/">status</a>\n', BASE)
    expect(html).toMatch(/<a href="https:\/\/weblate\.example\/" target="_blank" rel="noopener noreferrer">/)
  })

  it('leaves relative addresses as they are without a base', () => {
    expect(readmeToHtml('<img src="./a.png">\n')).toContain('src="./a.png"')
  })
})
