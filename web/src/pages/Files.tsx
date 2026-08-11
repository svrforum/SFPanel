import { useState, useEffect, useCallback, useRef, useMemo, Fragment } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Folder,
  File,
  FileText,
  Upload,
  FolderPlus,
  FilePlus2,
  RefreshCw,
  Download,
  Pencil,
  Trash2,
  ChevronRight,
  Home,
  Loader2,
  Search,
  Copy,
  X,
  type LucideIcon,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { usePrompt } from '@/components/PromptDialog'
import { formatBytes, formatDate, pathJoin } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  ContextMenu,
  ContextMenuTrigger,
  ContextMenuContent,
  ContextMenuItem,
  ContextMenuSeparator,
} from '@/components/ui/context-menu'
import { FileEditorDialog, type EditorTarget } from './files/components/FileEditorDialog'
import { isTextFile } from './files/components/fileLanguages'

import type { FileEntry } from '@/types/api'

// One per-row action list, rendered both as the row's icon buttons and as its
// context-menu items (previously two hand-synced copies of the same five actions).
interface EntryAction {
  key: string
  Icon: LucideIcon
  show: boolean
  /** Row button only — the context menu covers it via its open/edit item. */
  rowOnly?: boolean
  iconClassName?: string
  destructive?: boolean
  label: string
  menuLabel: string
  onClick: () => void
}

export default function Files() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const prompt = usePrompt()
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Core state
  const [currentPath, setCurrentPath] = useState('/')
  const [files, setFiles] = useState<FileEntry[]>([])
  const [loading, setLoading] = useState(true)
  const currentPathRef = useRef(currentPath)
  useEffect(() => { currentPathRef.current = currentPath }, [currentPath])

  // Editor dialog state
  const [editorTarget, setEditorTarget] = useState<EditorTarget | null>(null)

  // Upload state
  const [uploading, setUploading] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<{ fileName: string; percent: number } | null>(null)

  // Search state
  const [searchQuery, setSearchQuery] = useState('')
  const [searchActive, setSearchActive] = useState(false)
  const [searchResults, setSearchResults] = useState<FileEntry[]>([])
  const [searchTruncated, setSearchTruncated] = useState(false)
  const [searchCount, setSearchCount] = useState(0)
  const [searchLoading, setSearchLoading] = useState(false)

  // Multi-select state
  const [selectedPaths, setSelectedPaths] = useState<Set<string>>(new Set())
  const [bulkDeleting, setBulkDeleting] = useState(false)

  const fetchFiles = useCallback(async () => {
    try {
      setLoading(true)
      const data = await api.listFiles(currentPath)
      setFiles(data || [])
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.fetchFailed')
      toast.error(message)
      setFiles([])
    } finally {
      setLoading(false)
    }
  }, [currentPath, t])

  useEffect(() => {
    fetchFiles()
  }, [fetchFiles])

  // Clear selection and exit search mode whenever the directory changes
  useEffect(() => {
    setSelectedPaths(new Set())
    setSearchActive(false)
    setSearchResults([])
  }, [currentPath])

  // Search
  const exitSearch = () => {
    setSearchActive(false)
    setSearchQuery('')
    setSearchResults([])
    setSearchTruncated(false)
    setSearchCount(0)
    setSelectedPaths(new Set())
  }

  const handleSearch = async () => {
    const q = searchQuery.trim()
    if (!q) {
      exitSearch()
      return
    }
    setSearchLoading(true)
    setSelectedPaths(new Set())
    const pathAtStart = currentPathRef.current
    try {
      const data = await api.searchFiles(pathAtStart, q)
      if (currentPathRef.current !== pathAtStart) return
      setSearchResults(data.results || [])
      setSearchCount(data.count || 0)
      setSearchTruncated(!!data.truncated)
      setSearchActive(true)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.searchFailed')
      toast.error(message)
    } finally {
      setSearchLoading(false)
    }
  }

  // Copy
  const handleCopy = async (entry: FileEntry) => {
    const dir = entry.path.replace(/\/[^/]*$/, '') || '/'
    const dotIndex = entry.isDir ? -1 : entry.name.lastIndexOf('.')
    const suggested =
      dotIndex > 0
        ? `${entry.name.slice(0, dotIndex)}-copy${entry.name.slice(dotIndex)}`
        : `${entry.name}-copy`
    const dest = await prompt({
      title: t('files.copyTitle'),
      description: entry.path,
      defaultValue: pathJoin(dir, suggested),
      confirmLabel: t('files.copy'),
    })
    const trimmed = dest?.trim()
    if (!trimmed || trimmed === entry.path) return
    const pathAtStart = currentPathRef.current
    try {
      await api.copyPath(entry.path, trimmed)
      toast.success(t('files.copySuccess'))
      if (searchActive) {
        await handleSearch()
      } else if (currentPathRef.current === pathAtStart) {
        await fetchFiles()
      }
    } catch (err: unknown) {
      const status = (err as { status?: number })?.status
      if (status === 409) {
        toast.error(t('files.copyExists'))
      } else {
        const message = err instanceof Error ? err.message : t('files.copyFailed')
        toast.error(message)
      }
    }
  }

  // Multi-select helpers
  const toggleSelected = (path: string) => {
    setSelectedPaths((prev) => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  }

  const handleBulkDelete = async () => {
    const paths = Array.from(selectedPaths)
    if (paths.length === 0) return
    const ok = await confirm({
      title: t('files.deleteSelected'),
      description: t('files.deleteSelectedConfirm', { count: paths.length }),
      danger: true,
    })
    if (!ok) return
    setBulkDeleting(true)
    let succeeded = 0
    let failed = 0
    for (const p of paths) {
      try {
        await api.deletePath(p)
        succeeded += 1
      } catch {
        failed += 1
      }
    }
    setBulkDeleting(false)
    setSelectedPaths(new Set())
    toast.success(t('files.bulkDeleteResult', { succeeded, failed }))
    if (searchActive) {
      await handleSearch()
    } else {
      await fetchFiles()
    }
  }

  // Breadcrumb segments
  const pathSegments = currentPath
    .split('/')
    .filter((segment) => segment.length > 0)

  const navigateTo = (path: string) => {
    setCurrentPath(path)
  }

  const navigateToSegment = (index: number) => {
    if (index < 0) {
      navigateTo('/')
    } else {
      const path = '/' + pathSegments.slice(0, index + 1).join('/')
      navigateTo(path)
    }
  }

  const handleDirectoryClick = (entry: FileEntry) => {
    const newPath = pathJoin(currentPath, entry.name)
    navigateTo(newPath)
  }

  // Edit file
  const editMaxBytes = 5 * 1024 * 1024 // 5 MB; server also enforces similar cap
  const handleEditFile = async (entry: FileEntry) => {
    // Server caps /files/read at 5 MB. If the user opens a larger file the
    // editor open path produces a confusing 400 even after the size warning;
    // route them to download instead — that's almost always what they meant.
    // Single guard: every editor-open path funnels through this handler.
    if (entry.size > editMaxBytes) {
      const ok = await confirm({
        title: t('files.editorTooLarge', { size: Math.round(entry.size / 1024 / 1024) }),
        danger: true,
      })
      if (ok) void handleDownload(entry)
      return
    }
    setEditorTarget({ path: pathJoin(currentPath, entry.name), name: entry.name })
  }

  // Download file
  const handleDownload = async (entry: FileEntry) => {
    const filePath = pathJoin(currentPath, entry.name)
    try {
      const blob = await api.downloadFile(filePath)
      const url = URL.createObjectURL(blob)
      const link = document.createElement('a')
      link.href = url
      link.download = entry.name
      document.body.appendChild(link)
      link.click()
      document.body.removeChild(link)
      URL.revokeObjectURL(url)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.downloadFailed')
      toast.error(message)
    }
  }

  // New file
  const handleCreateFile = async () => {
    const name = await prompt({
      title: t('files.newFileTitle'),
      description: t('files.newFileDescription', { path: currentPath }),
      placeholder: t('files.fileNamePlaceholder'),
      confirmLabel: t('files.createFile'),
    })
    if (!name?.trim()) return
    const pathAtStart = currentPathRef.current
    try {
      await api.writeFile(pathJoin(pathAtStart, name.trim()), '')
      toast.success(t('files.fileCreated'))
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.fileCreateFailed')
      toast.error(message)
    }
  }

  // New folder
  const handleCreateFolder = async () => {
    const name = await prompt({
      title: t('files.newFolderTitle'),
      description: t('files.newFolderDescription', { path: currentPath }),
      placeholder: t('files.folderNamePlaceholder'),
      confirmLabel: t('files.createFolder'),
    })
    if (!name?.trim()) return
    const pathAtStart = currentPathRef.current
    try {
      await api.createDir(pathJoin(pathAtStart, name.trim()))
      toast.success(t('files.folderCreated'))
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.folderCreateFailed')
      toast.error(message)
    }
  }

  // Upload file
  const handleUploadClick = () => {
    fileInputRef.current?.click()
  }

  const handleFileSelected = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const selectedFiles = e.target.files
    if (!selectedFiles || selectedFiles.length === 0) return
    setUploading(true)
    const pathAtStart = currentPathRef.current
    try {
      for (const file of Array.from(selectedFiles)) {
        setUploadProgress({ fileName: file.name, percent: 0 })
        await api.uploadFile(pathAtStart, file, (percent) => {
          setUploadProgress({ fileName: file.name, percent })
        })
        toast.success(t('files.uploadSuccess', { name: file.name }))
      }
      setUploadProgress(null)
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      setUploadProgress(null)
      const message = err instanceof Error ? err.message : t('files.uploadFailed')
      toast.error(message)
    } finally {
      setUploading(false)
      if (fileInputRef.current) {
        fileInputRef.current.value = ''
      }
    }
  }

  // Delete
  const handleDelete = async (entry: FileEntry) => {
    const ok = await confirm({
      title: t('files.deleteTitle'),
      description: t('files.deleteConfirm', { name: entry.name }),
      danger: true,
    })
    if (!ok) return
    const pathAtStart = currentPathRef.current
    try {
      await api.deletePath(pathJoin(pathAtStart, entry.name))
      toast.success(t('files.deleteSuccess', { name: entry.name }))
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.deleteFailed')
      toast.error(message)
    }
  }

  // Rename
  const handleRename = async (entry: FileEntry) => {
    const newName = await prompt({
      title: t('files.renameTitle'),
      description: t('files.renameDescription', { name: entry.name }),
      defaultValue: entry.name,
      confirmLabel: t('files.renameAction'),
    })
    const trimmed = newName?.trim()
    if (!trimmed || trimmed === entry.name) return
    const pathAtStart = currentPathRef.current
    try {
      await api.renamePath(pathJoin(pathAtStart, entry.name), pathJoin(pathAtStart, trimmed))
      toast.success(t('files.renameSuccess'))
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.renameFailed')
      toast.error(message)
    }
  }

  // Path input editing state
  const [editingPath, setEditingPath] = useState(false)
  const [pathInput, setPathInput] = useState(currentPath)
  const pathInputRef = useRef<HTMLInputElement>(null)

  const handlePathEditStart = () => {
    setPathInput(currentPath)
    setEditingPath(true)
    setTimeout(() => pathInputRef.current?.select(), 0)
  }

  const handlePathSubmit = () => {
    const normalized = pathInput.trim() || '/'
    setEditingPath(false)
    if (normalized !== currentPath) {
      navigateTo(normalized.startsWith('/') ? normalized : '/' + normalized)
    }
  }

  const handlePathKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter') {
      handlePathSubmit()
    } else if (e.key === 'Escape') {
      setEditingPath(false)
    }
  }

  // Sort: directories first, then files, both alphabetical
  const sortedFiles = useMemo(() =>
    [...files].sort((a, b) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1
      return a.name.localeCompare(b.name)
    }), [files])

  // Rows currently shown: search results when searching, otherwise the directory listing.
  const displayedFiles = searchActive ? searchResults : sortedFiles

  // Resolve the absolute path of a displayed entry. Search results carry a full
  // `path`; normal listing entries are relative to currentPath.
  const entryPath = useCallback(
    (entry: FileEntry) => (searchActive ? entry.path : pathJoin(currentPath, entry.name)),
    [searchActive, currentPath],
  )

  // Open a search-result row: navigate into dirs, files jump to the parent dir.
  const handleSearchResultClick = (entry: FileEntry) => {
    if (entry.isDir) {
      const path = entry.path
      exitSearch()
      navigateTo(path)
      return
    }
    const dir = entry.path.replace(/\/[^/]*$/, '') || '/'
    exitSearch()
    navigateTo(dir)
  }

  // Open a row: navigate into dirs, open files in the editor.
  const openEntry = (entry: FileEntry) => {
    if (searchActive) {
      handleSearchResultClick(entry)
    } else if (entry.isDir) {
      handleDirectoryClick(entry)
    } else {
      void handleEditFile(entry)
    }
  }

  const entryActions = (entry: FileEntry): EntryAction[] => [
    {
      key: 'edit',
      Icon: Pencil,
      show: !searchActive && !entry.isDir,
      rowOnly: true,
      label: t('files.edit'),
      menuLabel: t('files.contextMenu.edit'),
      onClick: () => void handleEditFile(entry),
    },
    {
      key: 'download',
      Icon: Download,
      show: !searchActive && !entry.isDir,
      label: t('files.download'),
      menuLabel: t('files.contextMenu.download'),
      onClick: () => void handleDownload(entry),
    },
    {
      key: 'copy',
      Icon: Copy,
      show: true,
      label: t('files.copy'),
      menuLabel: t('files.copy'),
      onClick: () => void handleCopy(entry),
    },
    {
      key: 'rename',
      Icon: Pencil,
      iconClassName: 'h-3 w-3',
      show: !searchActive,
      label: t('files.rename'),
      menuLabel: t('files.contextMenu.rename'),
      onClick: () => void handleRename(entry),
    },
    {
      key: 'delete',
      Icon: Trash2,
      show: !searchActive,
      destructive: true,
      label: t('common.delete'),
      menuLabel: t('files.contextMenu.delete'),
      onClick: () => void handleDelete(entry),
    },
  ]

  return (
    <div className="space-y-4">
      <h1 className="text-[22px] font-bold tracking-tight">{t('files.title')}</h1>

      {/* Breadcrumb navigation / path input */}
      {editingPath ? (
        <div className="flex items-center gap-2">
          <Home className="h-4 w-4 text-muted-foreground shrink-0" />
          <Input
            ref={pathInputRef}
            value={pathInput}
            onChange={(e) => setPathInput(e.target.value)}
            onKeyDown={handlePathKeyDown}
            onBlur={handlePathSubmit}
            className="h-8 text-sm font-mono"
            placeholder="/"
            autoFocus
          />
        </div>
      ) : (
        <nav
          className="flex items-center gap-1 text-sm text-muted-foreground overflow-x-auto cursor-text rounded-md border border-transparent hover:border-border px-2 py-1.5 -mx-2 transition-colors"
          onClick={handlePathEditStart}
        >
          <button
            onClick={(e) => { e.stopPropagation(); navigateToSegment(-1) }}
            className="flex items-center gap-1 hover:text-foreground transition-colors shrink-0 rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
          >
            <Home className="h-4 w-4" />
            <span>/</span>
          </button>
          {pathSegments.map((segment, index) => (
            <span key={index} className="flex items-center gap-1 shrink-0">
              <ChevronRight className="h-3 w-3" />
              <button
                onClick={(e) => { e.stopPropagation(); navigateToSegment(index) }}
                className={
                  'rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0 ' +
                  (index === pathSegments.length - 1
                    ? 'font-medium text-foreground'
                    : 'hover:text-foreground transition-colors')
                }
              >
                {segment}
              </button>
            </span>
          ))}
          {/* Keyboard-accessible entry into path editing (the nav itself is click-only) */}
          <button
            onClick={(e) => { e.stopPropagation(); handlePathEditStart() }}
            title={t('files.editPath')}
            aria-label={t('files.editPath')}
            className="ml-1 shrink-0 p-0.5 hover:text-foreground transition-colors rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
          >
            <Pencil className="h-3 w-3" />
          </button>
        </nav>
      )}

      {/* Toolbar */}
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary shrink-0">
            {searchActive
              ? t('files.searchResultCount', { count: searchCount })
              : t('files.count', { count: files.length })}
          </span>
          <div className="relative">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
            <Input
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleSearch()
                else if (e.key === 'Escape' && searchActive) exitSearch()
              }}
              placeholder={t('files.searchPlaceholder')}
              className="h-8 w-56 pl-8 pr-8 text-sm"
            />
            {(searchActive || searchQuery) && (
              <button
                type="button"
                onClick={exitSearch}
                title={t('files.searchClear')}
                aria-label={t('files.searchClear')}
                className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground hover:text-foreground rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
              >
                {searchLoading ? <Loader2 className="h-4 w-4 animate-spin" /> : <X className="h-4 w-4" />}
              </button>
            )}
          </div>
        </div>
        <div className="flex items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            onClick={fetchFiles}
            disabled={loading}
          >
            <RefreshCw className={loading ? 'animate-spin' : ''} />
            {t('common.refresh')}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={handleCreateFile}
          >
            <FilePlus2 />
            {t('files.newFile')}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={handleCreateFolder}
          >
            <FolderPlus />
            {t('files.newFolder')}
          </Button>
          <Button
            size="sm"
            onClick={handleUploadClick}
            disabled={uploading}
          >
            {uploading ? (
              <Loader2 className="animate-spin" />
            ) : (
              <Upload />
            )}
            {t('files.upload')}
          </Button>
          <input
            ref={fileInputRef}
            type="file"
            multiple
            className="hidden"
            onChange={handleFileSelected}
          />
        </div>
      </div>

      {searchActive && searchTruncated && (
        <p className="text-[13px] text-muted-foreground">
          {t('files.searchTruncated', { count: searchResults.length })}
        </p>
      )}

      {/* Bulk action bar */}
      {selectedPaths.size > 0 && (
        <div className="flex items-center justify-between rounded-lg border bg-secondary/40 px-3 py-2">
          <span className="text-[13px] font-medium">
            {t('files.selectedCount', { count: selectedPaths.size })}
          </span>
          <div className="flex items-center gap-2">
            <Button variant="ghost" size="sm" onClick={() => setSelectedPaths(new Set())}>
              {t('common.cancel')}
            </Button>
            <Button variant="destructive" size="sm" onClick={handleBulkDelete} disabled={bulkDeleting}>
              {bulkDeleting ? <Loader2 className="animate-spin" /> : <Trash2 />}
              {t('files.deleteSelected')}
            </Button>
          </div>
        </div>
      )}

      {/* File listing table */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-10">
              <Checkbox
                checked={
                  displayedFiles.length > 0 &&
                  displayedFiles.every((e) => selectedPaths.has(entryPath(e)))
                }
                onCheckedChange={(checked) => {
                  if (checked) {
                    setSelectedPaths(new Set(displayedFiles.map((e) => entryPath(e))))
                  } else {
                    setSelectedPaths(new Set())
                  }
                }}
                aria-label={t('files.selectAll')}
              />
            </TableHead>
            <TableHead>{t('files.name')}</TableHead>
            <TableHead className="w-28">{t('files.size')}</TableHead>
            <TableHead className="w-44">{t('files.modified')}</TableHead>
            <TableHead className="w-28">{t('files.permissions')}</TableHead>
            <TableHead className="text-right w-36">{t('common.actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {displayedFiles.length === 0 && !loading && !searchLoading && (
            <TableRow>
              <TableCell colSpan={6} className="text-center text-muted-foreground py-8">
                {searchActive ? t('files.searchEmpty') : t('files.empty')}
              </TableCell>
            </TableRow>
          )}
          {loading && files.length === 0 && (
            <TableRow>
              <TableCell colSpan={6} className="text-center py-8">
                <div className="flex items-center justify-center gap-2 text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t('files.loading')}
                </div>
              </TableCell>
            </TableRow>
          )}
          {displayedFiles.map((entry) => {
            const rowPath = entryPath(entry)
            const actions = entryActions(entry)
            return (
            <ContextMenu key={rowPath}>
              <ContextMenuTrigger asChild>
                <TableRow
                  className="cursor-pointer hover:bg-secondary/50"
                  onClick={() => openEntry(entry)}
                >
                  <TableCell onClick={(e) => e.stopPropagation()}>
                    <Checkbox
                      checked={selectedPaths.has(rowPath)}
                      onCheckedChange={() => toggleSelected(rowPath)}
                      aria-label={entry.name}
                    />
                  </TableCell>
                  <TableCell>
                    {/* Real <button> so directories are keyboard-reachable (same
                        pattern as the DiskUsage rows); mouse users can still
                        click anywhere on the row. */}
                    <button
                      type="button"
                      onClick={(e) => { e.stopPropagation(); openEntry(entry) }}
                      className="flex items-center gap-2 w-full text-left rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                    >
                      {entry.isDir ? (
                        <Folder className="h-4 w-4 text-blue-500 shrink-0" />
                      ) : isTextFile(entry.name) ? (
                        <FileText className="h-4 w-4 text-amber-500 shrink-0" />
                      ) : (
                        <File className="h-4 w-4 text-muted-foreground shrink-0" />
                      )}
                      <div className="min-w-0">
                        <span className="truncate block">{entry.name}</span>
                        {searchActive && (
                          <span className="truncate block text-xs text-muted-foreground font-mono">{entry.path}</span>
                        )}
                      </div>
                    </button>
                  </TableCell>
                  <TableCell className="text-muted-foreground text-sm">
                    {entry.isDir ? '-' : formatBytes(entry.size)}
                  </TableCell>
                  <TableCell className="text-muted-foreground text-sm">
                    {formatDate(entry.modTime)}
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs font-mono">
                    {entry.mode || '-'}
                  </TableCell>
                  <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                    <div className="flex items-center justify-end gap-1">
                      {actions.filter((a) => a.show).map((a) => (
                        <Button
                          key={a.key}
                          variant="ghost"
                          size="icon-xs"
                          title={a.label}
                          aria-label={a.label}
                          onClick={a.onClick}
                        >
                          <a.Icon className={a.iconClassName} />
                        </Button>
                      ))}
                    </div>
                  </TableCell>
                </TableRow>
              </ContextMenuTrigger>
              <ContextMenuContent>
                <ContextMenuItem onClick={() => openEntry(entry)}>
                  {entry.isDir ? (
                    <Folder className="h-4 w-4" />
                  ) : (
                    <FileText className="h-4 w-4" />
                  )}
                  {entry.isDir
                    ? t('files.contextMenu.open')
                    : t('files.contextMenu.edit')}
                </ContextMenuItem>
                {actions.filter((a) => !a.rowOnly && a.show).map((a) => (
                  <Fragment key={a.key}>
                    {a.key === 'copy' && <ContextMenuSeparator />}
                    <ContextMenuItem
                      variant={a.destructive ? 'destructive' : undefined}
                      onClick={a.onClick}
                    >
                      <a.Icon className="h-4 w-4" />
                      {a.menuLabel}
                    </ContextMenuItem>
                  </Fragment>
                ))}
              </ContextMenuContent>
            </ContextMenu>
            )
          })}
        </TableBody>
      </Table>
      </div>

      {/* Background context menu (right-click on empty space below table) */}
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div className="min-h-[40px]" />
        </ContextMenuTrigger>
        <ContextMenuContent>
          <ContextMenuItem onClick={handleUploadClick}>
            <Upload className="h-4 w-4" />
            {t('files.upload')}
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem onClick={handleCreateFile}>
            <FilePlus2 className="h-4 w-4" />
            {t('files.newFile')}
          </ContextMenuItem>
          <ContextMenuItem onClick={handleCreateFolder}>
            <FolderPlus className="h-4 w-4" />
            {t('files.newFolder')}
          </ContextMenuItem>
          <ContextMenuSeparator />
          <ContextMenuItem onClick={fetchFiles}>
            <RefreshCw className="h-4 w-4" />
            {t('common.refresh')}
          </ContextMenuItem>
        </ContextMenuContent>
      </ContextMenu>

      {/* Edit file dialog */}
      <FileEditorDialog target={editorTarget} onOpenChange={(open) => { if (!open) setEditorTarget(null) }} />

      {/* Upload progress dialog */}
      <Dialog open={!!uploadProgress} onOpenChange={() => {}}>
        <DialogContent className="sm:max-w-md" onPointerDownOutside={(e) => e.preventDefault()}>
          <DialogHeader>
            <DialogTitle>{t('files.uploading')}</DialogTitle>
            <DialogDescription className="truncate">
              {uploadProgress?.fileName}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div className="h-1.5 rounded-full bg-secondary overflow-hidden">
              <div
                className="h-full rounded-full transition-all duration-300"
                style={{
                  width: `${uploadProgress?.percent ?? 0}%`,
                  backgroundColor: 'var(--primary)',
                }}
              />
            </div>
            <p className="text-center text-[13px] text-muted-foreground">
              {uploadProgress?.percent ?? 0}%
            </p>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  )
}
