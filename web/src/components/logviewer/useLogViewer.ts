import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useVirtualizer } from '@tanstack/react-virtual'
import {
  getParser,
  hasParsedView,
  parseLogLines,
  type LogEntry,
  type LogParser,
  type ParsedLogEntry,
} from '@/lib/logParsers'
import { getLogLevel, ROW_HEIGHT, type LogLevel } from './logViewUtils'

export interface UseLogViewerOptions {
  sourceId: string | null
  lines: string[]
  viewMode: 'raw' | 'parsed'
  autoScroll: boolean
}

/**
 * State machinery shared by the log viewer pages: parsed-entry memoization,
 * virtual scrolling, debounced search with match navigation, and the Ctrl+F
 * keyboard shortcut. Pair with <LogSearchBar viewer={…} /> and
 * <LogTable viewer={…} />.
 */
export function useLogViewer({ sourceId, lines, viewMode, autoScroll }: UseLogViewerOptions) {
  // Refs stay internal; consumers get callback-ref setters (setContainerEl /
  // setSearchInputEl) so no ref object is read during render.
  const containerRef = useRef<HTMLDivElement | null>(null)
  const searchInputRef = useRef<HTMLInputElement | null>(null)
  const setContainerEl = useCallback((el: HTMLDivElement | null) => {
    containerRef.current = el
  }, [])
  const setSearchInputEl = useCallback((el: HTMLInputElement | null) => {
    searchInputRef.current = el
  }, [])

  const [searchOpen, setSearchOpen] = useState(false)
  const [searchQuery, setSearchQuery] = useState('')
  const [currentMatch, setCurrentMatch] = useState(0)

  // Parsed log entries (memoized)
  const parsedEntries = useMemo<LogEntry[]>(() => {
    if (!sourceId || viewMode !== 'parsed' || !hasParsedView(sourceId)) return []
    return parseLogLines(sourceId, lines)
  }, [sourceId, viewMode, lines])

  const activeParser: LogParser<ParsedLogEntry> | null = sourceId ? getParser(sourceId) : null

  // Determine which data the virtualizer operates on
  const isParsedMode = viewMode === 'parsed' && !!activeParser && parsedEntries.length > 0
  const rowCount = !sourceId || lines.length === 0 ? 0 : isParsedMode ? parsedEntries.length : lines.length

  // Virtual scrolling
  const rowVirtualizer = useVirtualizer({
    count: rowCount,
    getScrollElement: () => containerRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 30,
  })

  // Auto-scroll when new lines arrive
  useEffect(() => {
    if (autoScroll && rowCount > 0) {
      rowVirtualizer.scrollToIndex(rowCount - 1, { align: 'end' })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [lines, autoScroll, rowCount])

  // Debounce searchQuery so typing in the search box doesn't trigger an
  // O(N) scan over 5000 log lines on every keystroke. 150ms feels
  // instant while keeping the scan rate ~7/sec at worst.
  const [debouncedQuery, setDebouncedQuery] = useState('')
  useEffect(() => {
    const id = setTimeout(() => setDebouncedQuery(searchQuery), 150)
    return () => clearTimeout(id)
  }, [searchQuery])

  // Memoized search: matching line indices + Set for O(1) lookup
  const matchingLines = useMemo(() => {
    if (!debouncedQuery) return [] as number[]
    const q = debouncedQuery.toLowerCase()
    return lines.reduce<number[]>((acc, line, i) => {
      if (line.toLowerCase().includes(q)) acc.push(i)
      return acc
    }, [])
  }, [debouncedQuery, lines])

  const matchingSet = useMemo(() => new Set(matchingLines), [matchingLines])

  // Memoized log levels for raw view (avoid per-row regex on each render)
  const logLevels = useMemo<Array<LogLevel | null>>(() => {
    if (isParsedMode) return []
    return lines.map(getLogLevel)
  }, [lines, isParsedMode])

  // Navigate to a virtual row by index
  const scrollToLine = useCallback(
    (lineIndex: number) => {
      rowVirtualizer.scrollToIndex(lineIndex, { align: 'center' })
    },
    [rowVirtualizer]
  )

  const goToMatch = useCallback(
    (direction: 'next' | 'prev') => {
      if (matchingLines.length === 0) return
      let next: number
      if (direction === 'next') {
        next = currentMatch + 1 >= matchingLines.length ? 0 : currentMatch + 1
      } else {
        next = currentMatch - 1 < 0 ? matchingLines.length - 1 : currentMatch - 1
      }
      setCurrentMatch(next)
      scrollToLine(matchingLines[next])
    },
    [matchingLines, currentMatch, scrollToLine]
  )

  // When search query changes, reset to first match and scroll
  useEffect(() => {
    if (matchingLines.length > 0) {
      setCurrentMatch(0)
      scrollToLine(matchingLines[0])
    } else {
      setCurrentMatch(0)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [searchQuery])

  const toggleSearch = useCallback(() => {
    if (!searchOpen) {
      setSearchOpen(true)
      setTimeout(() => searchInputRef.current?.focus(), 0)
    } else {
      setSearchOpen(false)
      setSearchQuery('')
    }
  }, [searchOpen])

  const closeSearch = useCallback(() => {
    setSearchOpen(false)
    setSearchQuery('')
  }, [])

  // Keyboard shortcut: Ctrl+F to open search
  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key === 'f') {
        e.preventDefault()
        setSearchOpen(true)
        setTimeout(() => searchInputRef.current?.focus(), 0)
      }
      if (e.key === 'Escape' && searchOpen) {
        setSearchOpen(false)
        setSearchQuery('')
      }
    }
    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [searchOpen])

  return {
    setContainerEl,
    setSearchInputEl,
    searchOpen,
    searchQuery,
    setSearchQuery,
    currentMatch,
    toggleSearch,
    closeSearch,
    goToMatch,
    matchingLines,
    matchingSet,
    logLevels,
    parsedEntries,
    activeParser,
    isParsedMode,
    rowVirtualizer,
  }
}

export type LogViewer = ReturnType<typeof useLogViewer>
