import { useMemo } from 'react'
import { Marked } from 'marked'
import DOMPurify from 'dompurify'

// escapeHtmlAttr escapes a value for safe interpolation into an HTML attribute
// (here: untrusted-README URLs). The rendered HTML is always run through
// DOMPurify before it touches the DOM (see RenderedReadme), so this is
// defense-in-depth: it keeps these string builders safe against
// attribute-breakout even if a future refactor moves or relaxes that sanitize
// step. Only URLs are escaped — link/image *text* may legitimately carry
// already-rendered inline HTML, so it is left for DOMPurify to clean.
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

function transformUrl(url: string, baseUrl?: string): string {
  if (!url) return url
  if (url.startsWith('http://') || url.startsWith('https://') || url.startsWith('data:')) return url
  if (baseUrl) {
    const cleanUrl = url.startsWith('./') ? url.slice(2) : url
    return baseUrl + cleanUrl
  }
  return url
}

function createMarked(baseUrl?: string): Marked {
  return new Marked({
    gfm: true,
    breaks: false,
    renderer: {
      // src/href are escaped at interpolation (escapeHtmlAttr); alt/text are
      // left for DOMPurify since they may carry rendered inline HTML. Output
      // always passes through DOMPurify in RenderedReadme before the DOM.
      image({ href, text }: { href: string; text: string }) {
        const src = transformUrl(href, baseUrl)
        const safeSrc = escapeHtmlAttr(src)
        const isBadge = src && (
          src.includes('shields.io') || src.includes('img.shields') ||
          src.includes('badge') || src.includes('contrib.rocks') ||
          src.includes('repobeats') || src.includes('star-history')
        )
        if (isBadge) {
          return `<img src="${safeSrc}" alt="${text}" class="inline-block h-5 my-0.5 mr-1 rounded-none" />`
        }
        const isLogo = (src && (src.endsWith('.svg') || src.includes('logo'))) ||
          (text && text.toLowerCase().includes('logo'))
        const maxH = isLogo ? 'max-h-20' : 'max-h-64'
        return `<img src="${safeSrc}" alt="${text}" class="max-w-full h-auto rounded-lg ${maxH}" />`
      },
      link({ href, text }: { href: string; text: string }) {
        const url = transformUrl(href, baseUrl)
        return `<a href="${escapeHtmlAttr(url)}" target="_blank" rel="noopener noreferrer">${text}</a>`
      },
    },
  })
}

export function RenderedReadme({ markdown, baseUrl }: { markdown: string; baseUrl?: string }) {
  const html = useMemo(() => {
    const processed = processGitHubAlerts(markdown)
    const md = createMarked(baseUrl)
    const raw = md.parse(processed) as string
    // README text comes from an untrusted external repo — sanitize before
    // injecting into the DOM. ADD_ATTR keeps our rendered anchors' target/rel
    // attributes (opener isolation), which DOMPurify drops by default.
    return DOMPurify.sanitize(raw, { ADD_ATTR: ['target', 'rel'] })
  }, [markdown, baseUrl])

  return (
    <div
      className="rounded-xl bg-secondary/20 p-5 prose prose-sm dark:prose-invert max-w-none
        prose-headings:text-foreground prose-headings:font-semibold
        prose-h1:text-[16px] prose-h1:mt-0 prose-h1:mb-2
        prose-h2:text-[14px] prose-h2:mt-5 prose-h2:mb-2
        prose-h3:text-[13px] prose-h3:mt-3 prose-h3:mb-1
        prose-p:text-[12px] prose-p:text-muted-foreground prose-p:leading-relaxed
        prose-li:text-[12px] prose-li:text-muted-foreground
        prose-strong:text-foreground prose-strong:font-medium
        prose-a:text-primary prose-a:no-underline hover:prose-a:underline
        prose-code:text-[11px] prose-code:bg-secondary/50 prose-code:px-1.5 prose-code:py-0.5 prose-code:rounded-md prose-code:text-foreground
        prose-pre:bg-[#1e1e2e] prose-pre:text-[#cdd6f4] prose-pre:rounded-xl prose-pre:p-4
        [&_pre_code]:bg-transparent [&_pre_code]:p-0 [&_pre_code]:text-[11px] [&_pre_code]:text-[#cdd6f4]
        prose-table:text-[11px]
        prose-th:text-[10px] prose-th:font-semibold prose-th:text-muted-foreground prose-th:uppercase prose-th:tracking-wider
        prose-td:text-[11px]
        prose-img:rounded-lg prose-img:my-2
        [&_img]:max-w-full [&_img]:h-auto [&_img]:rounded-lg
      "
      dangerouslySetInnerHTML={{ __html: html }}
    />
  )
}
