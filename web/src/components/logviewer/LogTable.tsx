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
  lines,
  loading,
  loadingText,
  emptyText,
  placeholder,
  className,
}: {
  viewer: LogViewer
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
    <div
      ref={setContainerEl}
      className={cn('min-h-[500px] overflow-auto rounded-b-xl border font-mono text-sm', className)}
      style={{ backgroundColor: '#1e1e1e' }}
    >
      {placeholder ? (
        <div className="flex items-center justify-center h-full min-h-[500px] text-gray-500">
          {placeholder}
        </div>
      ) : loading && lines.length === 0 ? (
        <div className="flex items-center justify-center h-full min-h-[500px] text-gray-500">
          <div className="text-center space-y-2">
            <RefreshCw className="h-8 w-8 mx-auto text-gray-600 animate-spin" />
            <p>{loadingText}</p>
          </div>
        </div>
      ) : lines.length === 0 ? (
        <div className="flex items-center justify-center h-full min-h-[500px] text-gray-500">
          <div className="text-center space-y-2">
            <FileText className="h-12 w-12 mx-auto text-gray-600" />
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
          <thead className="sticky top-0 z-10" style={{ backgroundColor: '#2d2d2d' }}>
            <tr>
              <th
                className="select-none text-right px-3 py-1.5 text-gray-500 border-r border-gray-700/50 border-b border-b-gray-700/50 whitespace-nowrap"
                style={{ fontSize: '11px' }}
              >
                #
              </th>
              {columns.map((col) => (
                <th
                  key={col.key}
                  className="text-left px-3 py-1.5 text-gray-400 border-b border-b-gray-700/50 whitespace-nowrap text-[11px] font-semibold uppercase tracking-wider"
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
                      className="select-none text-right px-3 py-0 text-gray-600 border-r border-gray-700/50 align-top whitespace-nowrap"
                      style={{ minWidth: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                    >
                      {index + 1}
                    </td>
                    <td
                      colSpan={columns.length}
                      className="px-3 py-0 whitespace-nowrap overflow-hidden text-ellipsis text-gray-400"
                      style={{ fontSize: '12px', lineHeight: '20px' }}
                      title={entry.rawLine}
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
                    className="select-none text-right px-3 py-0 text-gray-600 border-r border-gray-700/50 align-top whitespace-nowrap"
                    style={{ minWidth: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                  >
                    {index + 1}
                  </td>
                  {columns.map((col) => {
                    const rendered = col.render(entry as ParsedLogEntry)
                    return (
                      <td
                        key={col.key}
                        className={`px-3 py-0 text-left text-gray-200 whitespace-nowrap overflow-hidden ${col.flex ? 'text-ellipsis' : ''}`}
                        style={{ fontSize: '12px', lineHeight: '20px' }}
                        title={col.flex ? rendered.text : undefined}
                      >
                        {rendered.pill && rendered.color ? (
                          <span
                            className="inline-flex items-center px-1.5 py-0 rounded-full text-[10px] font-medium"
                            style={{
                              backgroundColor: `${rendered.color}20`,
                              color: rendered.color,
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
        /* Raw view — virtualized */
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
                      right: 0,
                      display: 'flex',
                    }}
                    className={`hover:bg-white/5 ${isCurrentMatch ? 'bg-yellow-500/20' : isMatch ? 'bg-yellow-500/10' : ''} ${levelBorder}`}
                  >
                    <td
                      className="select-none text-right px-3 py-0 text-gray-600 border-r border-gray-700/50 whitespace-nowrap shrink-0"
                      style={{ minWidth: '3.5rem', width: '3.5rem', fontSize: '12px', lineHeight: '20px' }}
                    >
                      {index + 1}
                    </td>
                    <td
                      className={`px-3 py-0 whitespace-nowrap overflow-hidden text-ellipsis flex-1 ${levelText}`}
                      style={{ fontSize: '12px', lineHeight: '20px' }}
                      title={line}
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
