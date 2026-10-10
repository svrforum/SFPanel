import { Marked, type Tokens } from 'marked'

// readmeToHtml turns an app's upstream README into HTML for the app store's
// detail page. The result is NOT safe yet: RenderedReadme runs it through
// DOMPurify before it reaches the DOM. Everything here is about addresses —
// a README is written for GitHub, where "./public/icon.svg" means a file in
// the repository, and on the panel it would mean /appstore/public/icon.svg.

// escapeHtmlAttr escapes a value for safe interpolation into an HTML attribute
// (here: untrusted-README URLs). Defence in depth: DOMPurify cleans the output
// anyway, but these string builders stay safe against attribute breakout even
// if that step moves. Only URLs are escaped — link/image *text* may carry
// already-rendered inline HTML, so it is left for DOMPurify.
function escapeHtmlAttr(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/"/g, '&quot;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

// Convert inline markdown (bold, links) to HTML
function inlineMarkdownToHtml(text: string): string {
  return text
    .replace(/\*\*(.+?)\*\*/g, '<strong>$1</strong>')
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, (_m, label: string, href: string) =>
      `<a href="${escapeHtmlAttr(href)}" target="_blank" rel="noopener noreferrer" class="underline">${label}</a>`)
}

// Convert GitHub Alert syntax to styled HTML
function processGitHubAlerts(markdown: string): string {
  const alertIcons: Record<string, string> = {
    NOTE: 'ℹ️',
    TIP: '💡',
    IMPORTANT: '❗',
    WARNING: '⚠️',
    CAUTION: '🔴',
  }
  return markdown.replace(
    /^> \[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]\s*\n((?:>.*\n?)*)/gm,
    (_match, type: string, body: string) => {
      const icon = alertIcons[type] || ''
      const content = inlineMarkdownToHtml(body.replace(/^> ?/gm, '').trim())
      const colors: Record<string, string> = {
        NOTE: 'border-blue-400 bg-blue-50 dark:bg-blue-950/30',
        TIP: 'border-green-400 bg-green-50 dark:bg-green-950/30',
        IMPORTANT: 'border-purple-400 bg-purple-50 dark:bg-purple-950/30',
        WARNING: 'border-yellow-400 bg-yellow-50 dark:bg-yellow-950/30',
        CAUTION: 'border-red-400 bg-red-50 dark:bg-red-950/30',
      }
      const color = colors[type] || 'border-gray-400 bg-gray-50'
      return `<div class="rounded-lg border-l-4 ${color} p-3 my-3 text-[12px]"><strong>${icon} ${type}</strong><br/>${content}</div>\n`
    }
  )
}

const ABSOLUTE = /^(?:[a-z][a-z0-9+.-]*:|\/\/)/i

// Where a README's relative addresses point. Images load from the raw file
// host; a link to a file (LICENSE, README_KO.md) opens GitHub's page for it
// rather than the bare text. Without a base nothing relative can be resolved.
interface Bases { files?: string; pages?: string }

function basesFor(baseUrl?: string): Bases {
  if (!baseUrl) return {}
  const raw = /^https:\/\/raw\.githubusercontent\.com\/([^/]+)\/([^/]+)\/([^/]+)\/(.*)$/.exec(baseUrl)
  const pages = raw ? `https://github.com/${raw[1]}/${raw[2]}/blob/${raw[3]}/${raw[4]}` : baseUrl
  return { files: baseUrl, pages }
}

function resolve(url: string, base?: string): string {
  if (!url || ABSOLUTE.test(url) || url.startsWith('#')) return url
  if (!base) return url
  return base + url.replace(/^\.\//, '').replace(/^\//, '')
}

// A srcset is "url descriptor, url descriptor".
function resolveSrcset(srcset: string, base?: string): string {
  return srcset
    .split(',')
    .map((part) => {
      const [url, ...rest] = part.trim().split(/\s+/)
      return [resolve(url, base), ...rest].join(' ')
    })
    .join(', ')
}

// Raw HTML in a README (logos in <div align=center>, <picture> banners, badge
// rows written as <a><img></a>) bypasses marked's image and link renderers, so
// its addresses are rewritten here. An in-page anchor (#-quick-start) loses
// its href: marked gives headings no ids to land on, and on the panel the
// fragment would only move the panel's own page.
// The value is already attribute text, entities and all, so only the base is
// put in front of it; DOMPurify still cleans the result.
function rewriteHtml(html: string, bases: Bases): string {
  return html.replace(/<(?:img|source|a)\b[^>]*>/gi, (tag) => newTab(tag.replace(
      /(\s)(src|srcset|href)(\s*=\s*)(["'])([^"']*)\4/gi,
      (all, space: string, attr: string, eq: string, quote: string, value: string) => {
        const name = attr.toLowerCase()
        if (name === 'href' && value.startsWith('#')) return ''
        const next = name === 'srcset'
          ? resolveSrcset(value, bases.files)
          : resolve(value, name === 'href' ? bases.pages : bases.files)
        return next === value ? all : `${space}${attr}${eq}${quote}${next}${quote}`
      },
    )),
  )
}

// A README's own <a> opens in a new tab like the rendered ones: without a
// target it would navigate the panel away.
function newTab(tag: string): string {
  if (!/^<a\b/i.test(tag)) return tag
  let out = tag
  if (!/\starget\s*=/i.test(out)) out = out.replace(/\s*\/?>$/, ' target="_blank">')
  if (!/\srel\s*=/i.test(out)) out = out.replace(/\s*\/?>$/, ' rel="noopener noreferrer">')
  return out
}

function isBadge(src: string): boolean {
  return src.includes('shields.io') || src.includes('img.shields') ||
    src.includes('badge') || src.includes('contrib.rocks') ||
    src.includes('repobeats') || src.includes('star-history')
}

export function readmeToHtml(markdown: string, baseUrl?: string): string {
  const bases = basesFor(baseUrl)
  const marked = new Marked({
    gfm: true,
    breaks: false,
    renderer: {
      // src/href are escaped at interpolation; alt/text are left for
      // DOMPurify since they may carry rendered inline HTML.
      image({ href, text }: Tokens.Image) {
        const src = escapeHtmlAttr(resolve(href, bases.files))
        if (isBadge(src)) {
          return `<img src="${src}" alt="${text}" class="inline-block h-5 my-0.5 mr-1 rounded-none" />`
        }
        const isLogo = src.endsWith('.svg') || src.includes('logo') || text.toLowerCase().includes('logo')
        return `<img src="${src}" alt="${text}" class="max-w-full h-auto rounded-lg ${isLogo ? 'max-h-20' : 'max-h-64'}" />`
      },
      // The link's own content is rendered, not its source text: a badge that
      // links somewhere ([![Sponsors](…)](…)) came out as the literal markdown.
      link(token: Tokens.Link) {
        const inner = this.parser.parseInline(token.tokens)
        if (token.href.startsWith('#')) return inner
        const href = escapeHtmlAttr(resolve(token.href, bases.pages))
        return `<a href="${href}" target="_blank" rel="noopener noreferrer">${inner}</a>`
      },
      html({ text }: Tokens.HTML | Tokens.Tag) {
        return rewriteHtml(text, bases)
      },
    },
  })
  return marked.parse(processGitHubAlerts(markdown)) as string
}
