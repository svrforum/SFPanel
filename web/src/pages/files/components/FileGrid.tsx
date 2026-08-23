import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { File, FileArchive, FileText, Folder, Image as ImageIcon, Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { Checkbox } from '@/components/ui/checkbox'
import { cn, formatBytes } from '@/lib/utils'
import {
  ContextMenu,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
  ContextMenuTrigger,
} from '@/components/ui/context-menu'
import type { FileEntry } from '@/types/api'
import type { EntryAction } from '../entryActions'
import { useInnerTrigger } from '../innerTrigger'

/** Formats the panel can render a thumbnail for. Anything else gets an icon. */
const THUMBNAILABLE = /\.(jpe?g|png|gif)$/i

function TileIcon({ entry }: { entry: FileEntry }) {
  const cls = 'h-10 w-10'
  if (entry.isDir) return <Folder className={cn(cls, 'text-blue-500')} aria-hidden="true" />
  if (entry.kind === 'image') return <ImageIcon className={cn(cls, 'text-violet-500')} aria-hidden="true" />
  if (/\.(zip|tar|tgz|gz)$/i.test(entry.name)) return <FileArchive className={cn(cls, 'text-emerald-600')} aria-hidden="true" />
  if (entry.kind === 'binary') return <File className={cn(cls, 'text-muted-foreground')} aria-hidden="true" />
  return <FileText className={cn(cls, 'text-amber-500')} aria-hidden="true" />
}

/**
 * The thumbnail, or the icon it falls back to.
 *
 * Falling back on error rather than pre-checking every case: a JPEG can still
 * fail to render — a truncated download, a file the panel refuses as too large
 * — and a broken-image glyph in a grid of two hundred tiles is worse than an
 * icon that was always going to be fine.
 */
function Thumbnail({ entry }: { entry: FileEntry }) {
  const [failed, setFailed] = useState(false)
  const canThumb = !entry.isDir && THUMBNAILABLE.test(entry.name)

  if (!canThumb || failed) {
    return (
      <div className="flex h-full w-full items-center justify-center">
        <TileIcon entry={entry} />
      </div>
    )
  }
  return (
    <img
      src={api.thumbnailUrl(entry.path, 192)}
      alt=""
      // Native lazy loading rather than an observer: the browser already knows
      // where the viewport is, and a grid of tiles is exactly what the
      // attribute exists for.
      loading="lazy"
      decoding="async"
      onError={() => setFailed(true)}
      className="h-full w-full rounded-lg object-cover"
    />
  )
}

/**
 * A grid of tiles, for looking at a directory rather than reading it.
 *
 * The list view is better for almost everything — it shows size, date, owner
 * and permissions at a glance, and fits more rows on a screen. What it cannot
 * do is show you a photo. That is the whole reason this exists, which is also
 * why it renders real thumbnails rather than larger icons: a grid of icons is
 * a list with less information in it.
 */
export function FileGrid({
  entries,
  loading,
  emptyMessage,
  selectedPaths,
  entryPath,
  onToggleSelect,
  onOpen,
  actionsFor,
  activeIndex,
}: {
  entries: FileEntry[]
  loading: boolean
  emptyMessage: string
  selectedPaths: Set<string>
  entryPath: (entry: FileEntry) => string
  onToggleSelect: (path: string) => void
  onOpen: (entry: FileEntry) => void
  actionsFor: (entry: FileEntry) => EntryAction[]
  activeIndex: number
}) {
  const { t } = useTranslation()

  if (loading && entries.length === 0) {
    return (
      <div className="flex items-center justify-center gap-2 py-16 text-[13px] text-muted-foreground">
        <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
        {t('files.loading')}
      </div>
    )
  }
  if (entries.length === 0) {
    return <div className="py-16 text-center text-[13px] text-muted-foreground">{emptyMessage}</div>
  }

  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-3 md:grid-cols-4 lg:grid-cols-6">
      {entries.map((entry, index) => (
        <GridTile
          key={entryPath(entry)}
          entry={entry}
          selected={selectedPaths.has(entryPath(entry))}
          active={index === activeIndex}
          onToggleSelect={() => onToggleSelect(entryPath(entry))}
          onOpen={() => onOpen(entry)}
          actions={actionsFor(entry).filter((a) => a.show)}
        />
      ))}
    </div>
  )
}

function GridTile({ entry, selected, active, onToggleSelect, onOpen, actions }: {
  entry: FileEntry
  selected: boolean
  active: boolean
  onToggleSelect: () => void
  onOpen: () => void
  actions: EntryAction[]
}) {
  const innerTrigger = useInnerTrigger()

  return (
    <ContextMenu>
      <ContextMenuTrigger asChild>
        <div
          className={cn(
            'group relative rounded-2xl bg-card p-2 card-shadow transition-colors',
            selected && 'ring-2 ring-primary',
            active && !selected && 'ring-1 ring-ring/40',
          )}
          {...innerTrigger}
        >
          {/* A 44px target around a 16px checkbox. It sits over the tile
              rather than beside it so the thumbnail keeps the full width. */}
          <label
            className={cn(
              'absolute left-1 top-1 z-10 flex h-11 w-11 items-center justify-center',
              // Always visible once selected, or the only way to clear a
              // selection on touch would be to hover something.
              !selected && 'opacity-0 focus-within:opacity-100 group-hover:opacity-100',
            )}
            onClick={(e) => e.stopPropagation()}
          >
            <Checkbox checked={selected} onCheckedChange={onToggleSelect} aria-label={entry.name} />
          </label>

          <button
            type="button"
            onClick={onOpen}
            className="w-full rounded-xl outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          >
            <div className="aspect-square w-full overflow-hidden rounded-lg bg-secondary/40">
              <Thumbnail entry={entry} />
            </div>
            <p className="mt-1.5 line-clamp-2 break-all text-center text-[12px] leading-tight">
              {entry.name}
            </p>
            <p className="text-center text-[11px] text-muted-foreground">
              {entry.isDir ? '' : formatBytes(entry.size)}
            </p>
          </button>
        </div>
      </ContextMenuTrigger>

      <ContextMenuContent>
        {actions.map((a, i) => (
          <div key={a.key}>
            {a.destructive && i > 0 && <ContextMenuSeparator />}
            <ContextMenuItem variant={a.destructive ? 'destructive' : undefined} onClick={a.onClick}>
              <a.Icon className="h-4 w-4" />
              {a.menuLabel}
            </ContextMenuItem>
          </div>
        ))}
      </ContextMenuContent>
    </ContextMenu>
  )
}
