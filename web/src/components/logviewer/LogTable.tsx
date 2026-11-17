import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { FileText, RefreshCw } from 'lucide-react'
import { cn } from '@/lib/utils'
import type { ColumnDef, ParsedLogEntry } from '@/lib/logParsers'
import {
  highlightText,
  LOG_LEVEL_COLORS,
  LOG_LEVEL_TEXT_COLORS,
  ROW_HEIGHT,
} from './logViewUtils'
import type { LogViewer } from './useLogViewer'

/**
 * The virtualized log table shared by the Logs page and the Firewall > Logs
 * tab: column-driven parsed rendering (pills, level colors, search-match
 * highlighting) plus the raw line view. The scroll container carries the
 * viewer's containerRef so search navigation and tail-follow work.
 */
export function LogTable({
  viewer,
  label,
  lines,
  loading,
  loadingText,
  emptyText,
  placeholder,
  className,
}: {
  viewer: LogViewer
  /** Names the scrollable log region for a screen reader (the source's name). */
  label: string
  lines: string[]
  loading: boolean
  loadingText: string
  emptyText: string
  /** Rendered centered instead of any table content (Logs' "select a source" state). */
  placeholder?: ReactNode
  className?: string
}) {
  const { t } = useTranslation()
  const {
    setContainerEl,
    rowVirtualizer,
    parsedEntries,
    activeParser,
    isParsedMode,
    matchingSet,
    matchingLines,
    currentMatch,
    searchQuery,
    logLevels,
  } = viewer

  const virtualItems = rowVirtualizer.getVirtualItems()
  const totalSize = rowVirtualizer.getTotalSize()
  const columns = (activeParser?.columns ?? []) as ColumnDef<ParsedLogEntry>[]

  return (
    // The terminal's own surface tokens: the hard-coded #1e1e1e was a second
    // dark log surface beside --terminal, and its greys failed contrast on it.
    // A focusable region, so the keyboard can scroll it too. Its floor gives
    // way on a short screen, where 500px was nearly the whole of it.
    <div
      ref={setContainerEl}
      tabIndex={0}
      role="region"
      aria-label={label}
      className={cn('min-h-[min(500px,60dvh)] overflow-auto rounded-b-xl border bg-terminal text-terminal-foreground font-mono text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring', className)}
    >
      {placeholder ? (
        <div className="flex items-center justify-center h-full min-h-[min(500px,60dvh)] text-gray-400">
          {placeholder}
        </div>
      ) : loading && lines.length === 0 ? (
        <div className="flex items-center justify-center h-full min-h-[min(500px,60dvh)] text-gray-400">
          <div className="text-center space-y-2">
            <RefreshCw className="h-8 w-8 mx-auto text-gray-500 animate-spin" />
            <p>{loadingText}</p>
          </div>
        </div>
      ) : lines.length === 0 ? (
        <div className="flex items-center justify-center h-full min-h-[min(500px,60dvh)] text-gray-400">
          <div className="text-center space-y-2">
            <FileText className="h-12 w-12 mx-auto text-gray-500" />
            <p>{emptyText}</p>
          </div>
        </div>
      ) : isParsedMode ? (
        <table className="border-collapse" style={{ tableLayout: 'fixed' }}>
          <colgroup>
            <col style={{ width: '3.5rem' }} />
            {columns.map((col) => (
              <col key={col.key} style={{ width: col.width }} />
            ))}
          </colgroup>
          <thead className="sticky top-0 z-10 bg-[color-mix(in_srgb,var(--terminal)_94%,white)]">
            <tr>
              <th
                className="select-none text-right px-3 py-1.5 text-gray-400 border-r border-gray-700/50 border-b border-b-gray-700/50 whitespace-nowrap"
                style={{ fontSize: '12px' }}
              >
                #
              </th>
              {columns.map((col) => (
                <th
                  key={col.key}
                  className="text-left px-3 py-1.5 text-gray-400 border-b border-b-gray-700/50 whitespace-nowrap text-[12px] font-semibold uppercase tracking-wider"
                >
                  {t(col.i18nKey)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {/* Top spacer for virtual scroll */}
            {virtualItems.length > 0 && virtualItems[0].start > 0 && (
              <tr><td colSpan={columns.length + 1} style={{ height: virtualItems[0].start, padding: 0, border: 0 }} /></tr>
            )}
            {virtualItems.map((virtualRow) => {
              const index = virtualRow.index
              const entry = parsedEntries[index]
              const isMatch = matchingSet.has(index)
              const isCurrentMatch = isMatch && matchingLines[currentMatch] === index

              if (!entry.parsed) {
                return (
                  <tr
                    key={virtualRow.key}
                    data-line={index}
                    style={{ height: ROW_HEIGHT }}
                    className={`hover:bg-white/5 ${isCurrentMatch ? 'bg-yellow-500/20' : isMatch ? 'bg-yellow-500/10' : ''}`}
                  >
                    <td
                      className="select-none text-right px-3 py-0 text-gray-400 border-r border-gray-700/50 align-top whitespace-nowrap"
                      style={{ minWidth: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                    >
                      {index + 1}
                    </td>
                    <td
                      colSpan={columns.length}
                      className="px-3 py-0 whitespace-nowrap text-gray-400"
                      style={{ fontSize: '12px', lineHeight: '20px' }}
                    >
                      {searchQuery && isMatch ? highlightText(entry.rawLine, searchQuery) : entry.rawLine}
                    </td>
                  </tr>
                )
              }

              return (
                <tr
                  key={virtualRow.key}
                  data-line={index}
                  style={{ height: ROW_HEIGHT }}
                  className={`hover:bg-white/5 ${isCurrentMatch ? 'bg-yellow-500/20' : isMatch ? 'bg-yellow-500/10' : ''}`}
                >
                  <td
                    className="select-none text-right px-3 py-0 text-gray-400 border-r border-gray-700/50 align-top whitespace-nowrap"
                    style={{ minWidth: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                  >
                    {index + 1}
                  </td>
                  {columns.map((col) => {
                    const rendered = col.render(entry as ParsedLogEntry)
                    return (
                      <td
                        key={col.key}
                        className={`px-3 py-0 text-left text-gray-200 whitespace-nowrap ${col.flex ? '' : 'overflow-hidden'}`}
                        style={{ fontSize: '12px', lineHeight: '20px' }}
                      >
                        {rendered.pill && rendered.color ? (
                          <span
                            className="inline-flex items-center px-1.5 py-0 rounded-full text-[12px] font-medium"
                            // The palette's own hex read 3.0-3.9:1 on its tint
                            // over the terminal surface; seven parts in ten of
                            // it with white reads 5.3:1 or more on both themes.
                            style={{
                              backgroundColor: `${rendered.color}20`,
                              color: `color-mix(in srgb, ${rendered.color} 70%, white)`,
                            }}
                          >
                            {rendered.text}
                          </span>
                        ) : col.flex ? (
                          <span className="text-gray-300">
                            {searchQuery && isMatch ? highlightText(rendered.text, searchQuery) : rendered.text}
                          </span>
                        ) : (
                          <span>{rendered.text}</span>
                        )}
                      </td>
                    )
                  })}
                </tr>
              )
            })}
            {/* Bottom spacer for virtual scroll */}
            {virtualItems.length > 0 && (
              <tr><td colSpan={columns.length + 1} style={{ height: totalSize - (virtualItems[virtualItems.length - 1]?.end ?? 0), padding: 0, border: 0 }} /></tr>
            )}
          </tbody>
        </table>
      ) : (
        /* Raw view — virtualized. A row is as wide as its line (at least the
           view), and the region scrolls sideways to its end: an ellipsis and
           a title left the rest of the line to a mouse hover alone. */
        <div style={{ height: totalSize, position: 'relative' }}>
          <table className="w-full border-collapse" style={{ position: 'absolute', top: 0, left: 0, right: 0 }}>
            <tbody>
              {virtualItems.map((virtualRow) => {
                const index = virtualRow.index
                const line = lines[index]
                const isMatch = matchingSet.has(index)
                const isCurrentMatch = isMatch && matchingLines[currentMatch] === index
                const level = logLevels[index]
                const levelBorder = level ? LOG_LEVEL_COLORS[level] : ''
                const levelText = level ? LOG_LEVEL_TEXT_COLORS[level] : 'text-gray-200'
                return (
                  <tr
                    key={virtualRow.key}
                    data-line={index}
                    style={{
                      height: ROW_HEIGHT,
                      position: 'absolute',
                      top: virtualRow.start,
                      left: 0,
                      minWidth: '100%',
                      display: 'flex',
                    }}
                    className={`hover:bg-white/5 ${isCurrentMatch ? 'bg-yellow-500/20' : isMatch ? 'bg-yellow-500/10' : ''} ${levelBorder}`}
                  >
                    <td
                      className="select-none text-right px-3 py-0 text-gray-400 border-r border-gray-700/50 whitespace-nowrap shrink-0"
                      style={{ minWidth: '3.5rem', width: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                    >
                      {index + 1}
                    </td>
                    <td
                      className={`px-3 py-0 whitespace-nowrap flex-1 ${levelText}`}
                      style={{ fontSize: '12px', lineHeight: '20px' }}
                    >
                      {searchQuery && isMatch ? highlightText(line, searchQuery) : line}
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  )
}
