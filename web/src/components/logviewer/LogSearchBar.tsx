import { useTranslation } from 'react-i18next'
import { ChevronDown, ChevronUp, Search, X } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { LogViewer } from './useLogViewer'

// In-viewer search bar (query input, match counter, prev/next/close). Renders
// nothing while the viewer's search is closed.
export function LogSearchBar({ viewer, className }: { viewer: LogViewer; className?: string }) {
  const { t } = useTranslation()
  const {
    searchOpen,
    searchQuery,
    setSearchQuery,
    setSearchInputEl,
    matchingLines,
    currentMatch,
    goToMatch,
    closeSearch,
  } = viewer

  if (!searchOpen) return null

  return (
    <div className={cn('flex items-center gap-2 px-3 py-2 bg-secondary/50 border-0 rounded-xl', className)}>
      <Search className="h-4 w-4 text-muted-foreground shrink-0" />
      <Input
        ref={setSearchInputEl}
        value={searchQuery}
        onChange={(e) => setSearchQuery(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter') {
            e.preventDefault()
            goToMatch(e.shiftKey ? 'prev' : 'next')
          }
          if (e.key === 'Escape') {
            closeSearch()
          }
        }}
        placeholder={t('logs.searchPlaceholder')}
        className="h-7 text-sm flex-1"
        autoFocus
      />
      {searchQuery && (
        <span className="text-xs text-muted-foreground whitespace-nowrap">
          {matchingLines.length > 0
            ? `${currentMatch + 1} / ${matchingLines.length}`
            : t('logs.noMatches')}
        </span>
      )}
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={() => goToMatch('prev')}
        disabled={matchingLines.length === 0}
        title={t('logs.prevMatch')}
        aria-label={t('logs.prevMatch')}
      >
        <ChevronUp className="h-4 w-4" />
      </Button>
      <Button
        variant="ghost"
        size="icon-xs"
        onClick={() => goToMatch('next')}
        disabled={matchingLines.length === 0}
        title={t('logs.nextMatch')}
        aria-label={t('logs.nextMatch')}
      >
        <ChevronDown className="h-4 w-4" />
      </Button>
      <Button variant="ghost" size="icon-xs" onClick={closeSearch} aria-label={t('common.close')}>
        <X className="h-4 w-4" />
      </Button>
    </div>
  )
}
