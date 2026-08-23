import { useTranslation } from 'react-i18next'
import { File, FileText, Folder, Image as ImageIcon, Loader2, MoreVertical } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { formatBytes, formatDate } from '@/lib/utils'
import type { FileEntry } from '@/types/api'
import type { EntryAction } from '../entryActions'

function EntryIcon({ entry }: { entry: FileEntry }) {
  if (entry.isDir) return <Folder className="h-5 w-5 shrink-0 text-blue-500" aria-hidden="true" />
  if (entry.kind === 'image') return <ImageIcon className="h-5 w-5 shrink-0 text-violet-500" aria-hidden="true" />
  if (entry.kind === 'binary') return <File className="h-5 w-5 shrink-0 text-muted-foreground" aria-hidden="true" />
  return <FileText className="h-5 w-5 shrink-0 text-amber-500" aria-hidden="true" />
}

/**
 * The phone view of a directory.
 *
 * The page shipped one table for every width. A six-column table does not fit
 * 390 pixels, so the columns that matter — size, modified, permissions — were
 * simply off-screen, and the five row actions were 24-pixel targets in a row
 * with Delete immediately beside Rename. Every other list page in this panel
 * already solves this with cards; this is that pattern applied here.
 *
 * The actions collapse into one menu rather than shrinking further. A row of
 * tiny targets is not a small version of a toolbar, it is a way to delete the
 * wrong thing.
 */
export function FileCardList({
  entries,
  loading,
  emptyMessage,
  selectedPaths,
  entryPath,
  onToggleSelect,
  onOpen,
  actionsFor,
  searchActive,
}: {
  entries: FileEntry[]
  loading: boolean
  emptyMessage: string
  selectedPaths: Set<string>
  entryPath: (entry: FileEntry) => string
  onToggleSelect: (path: string) => void
  onOpen: (entry: FileEntry) => void
  actionsFor: (entry: FileEntry) => EntryAction[]
  searchActive: boolean
}) {
  const { t } = useTranslation()

  if (loading && entries.length === 0) {
    return (
      <div className="flex items-center justify-center gap-2 py-10 text-[13px] text-muted-foreground md:hidden">
        <Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
        {t('files.loading')}
      </div>
    )
  }

  if (entries.length === 0) {
    return (
      <div className="py-10 text-center text-[13px] text-muted-foreground md:hidden">{emptyMessage}</div>
    )
  }

  return (
    <div className="space-y-2 md:hidden">
      {entries.map((entry) => {
        const rowPath = entryPath(entry)
        const actions = actionsFor(entry).filter((a) => a.show)
        return (
          <div key={rowPath} className="rounded-2xl bg-card p-3 card-shadow">
            <div className="flex items-start gap-3">
              {/* A 44px hit area around the checkbox: the desktop one is a
                  16px square, which is a coin-flip with a thumb. */}
              <label className="flex h-11 w-11 shrink-0 items-center justify-center">
                <Checkbox
                  checked={selectedPaths.has(rowPath)}
                  onCheckedChange={() => onToggleSelect(rowPath)}
                  aria-label={entry.name}
                />
              </label>

              <button
                type="button"
                onClick={() => onOpen(entry)}
                className="min-w-0 flex-1 rounded-lg py-1 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
              >
                <div className="flex items-center gap-2">
                  <EntryIcon entry={entry} />
                  <span className="truncate text-[14px] font-medium">{entry.name}</span>
                </div>
                {searchActive && (
                  <p className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">{entry.path}</p>
                )}
                {entry.linkTarget && (
                  <p className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">→ {entry.linkTarget}</p>
                )}
                <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-0.5 text-[11px] text-muted-foreground">
                  <span>{entry.isDir ? t('files.folder', { defaultValue: 'Folder' }) : formatBytes(entry.size)}</span>
                  <span>{formatDate(entry.modTime)}</span>
                  <span className="font-mono">{entry.mode || '-'}</span>
                  {entry.owner && (
                    <span className="font-mono">
                      {entry.owner.user || entry.owner.uid}:{entry.owner.group || entry.owner.gid}
                    </span>
                  )}
                </div>
              </button>

              {actions.length > 0 && (
                <DropdownMenu>
                  <DropdownMenuTrigger asChild>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-11 w-11 shrink-0 rounded-xl"
                      aria-label={t('common.actions')}
                    >
                      <MoreVertical className="h-4 w-4" />
                    </Button>
                  </DropdownMenuTrigger>
                  <DropdownMenuContent align="end">
                    {actions.map((a, i) => (
                      <div key={a.key}>
                        {/* Put the destructive item behind a divider so a
                            mis-tap lands on a separator, not on Delete. */}
                        {a.destructive && i > 0 && <DropdownMenuSeparator />}
                        <DropdownMenuItem
                          variant={a.destructive ? 'destructive' : undefined}
                          onClick={a.onClick}
                        >
                          <a.Icon className="h-4 w-4" />
                          {a.menuLabel}
                        </DropdownMenuItem>
                      </div>
                    ))}
                  </DropdownMenuContent>
                </DropdownMenu>
              )}
            </div>
          </div>
        )
      })}
    </div>
  )
}
