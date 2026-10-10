import { memo, useMemo } from 'react'
import DOMPurify from 'dompurify'
import { readmeToHtml } from '../readmeHtml'

// memo, and one { __html } object per README: React 19 rewrites innerHTML
// whenever the object it is handed is a new one, even with the same string,
// and every rewrite rebuilt the README's images. While an install streamed
// its log the dialog re-rendered on each line, so a missing image was
// requested again each time — hundreds of 404s for one open dialog.
export const RenderedReadme = memo(function RenderedReadme({ markdown, baseUrl }: { markdown: string; baseUrl?: string }) {
  const inner = useMemo(() => ({
    // README text comes from an untrusted external repo — sanitize before
    // injecting into the DOM. ADD_ATTR keeps the anchors' target/rel
    // attributes (opener isolation), which DOMPurify drops by default.
    __html: DOMPurify.sanitize(readmeToHtml(markdown, baseUrl), { ADD_ATTR: ['target', 'rel'] }),
  }), [markdown, baseUrl])

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
      dangerouslySetInnerHTML={inner}
    />
  )
})
