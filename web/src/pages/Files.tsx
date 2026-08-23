import { useState, useEffect, useCallback, useRef, useMemo, Fragment } from 'react'
import { useTranslation } from 'react-i18next'
import { useSearchParams } from 'react-router-dom'
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
  ArrowUp,
  ArrowDown,
  FolderInput,
  Eye,
  EyeOff,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { usePrompt } from '@/components/PromptDialog'
import { formatBytes, formatDate, pathJoin, downloadBlob } from '@/lib/utils'
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
  DialogFooter,
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
import { FilePreviewDialog, type PreviewTarget } from './files/components/FilePreviewDialog'
import { isTextFile } from './files/components/fileLanguages'
import { useFileView, useVisibleEntries, sortEntries, type SortKey } from './files/useFileView'
import { FileCardList } from './files/components/FileCardList'
import { FolderPickerDialog } from './files/components/FolderPickerDialog'
import type { EntryAction } from './files/entryActions'

import type { FileEntry } from '@/types/api'


export default function Files() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const prompt = usePrompt()
  const fileInputRef = useRef<HTMLInputElement>(null)

  // The directory lives in the URL, not only in component state.
  //
  // It used to be state alone, so the browser's Back button had nothing to go
  // back to: three folders deep, Back left the file manager entirely rather
  // than stepping up one level. The URL was also unshareable — "look at
  // /opt/stacks/immich" meant describing the clicks — and a refresh dropped
  // you at /.
  const [searchParams, setSearchParams] = useSearchParams()
  const pathParam = searchParams.get('path') || '/'

  // Core state
  const [currentPath, setCurrentPath] = useState(pathParam)
  const [files, setFiles] = useState<FileEntry[]>([])
  const [loading, setLoading] = useState(true)
  const currentPathRef = useRef(currentPath)
  useEffect(() => { currentPathRef.current = currentPath }, [currentPath])

  // Follow the URL when it changes underneath us — a Back/Forward press, or a
  // link someone pasted. Writing the URL happens in navigateTo; this is the
  // read side, and it must not write back or the two would ping-pong.
  useEffect(() => {
    setCurrentPath((prev) => (prev === pathParam ? prev : pathParam))
  }, [pathParam])

  // Editor dialog state
  const [editorTarget, setEditorTarget] = useState<EditorTarget | null>(null)
  const [previewTarget, setPreviewTarget] = useState<PreviewTarget | null>(null)
  // Entries queued for a move, and the picker that chooses where. Move is the
  // one operation the server has always supported and the UI never offered:
  // rename joined the new name onto the current directory, so it could not
  // leave it.
  const [moveTargets, setMoveTargets] = useState<FileEntry[] | null>(null)

  // Upload state
  const [uploading, setUploading] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<
    { fileName: string; percent: number; index: number; total: number } | null
  >(null)
  const uploadAbortRef = useRef<AbortController | null>(null)
  // Drop-target depth. dragenter/dragleave fire for every child element the
  // pointer crosses, so a boolean flickers the overlay off the moment the
  // cursor moves over a row; counting keeps it stable.
  const dragDepth = useRef(0)
  const [dragActive, setDragActive] = useState(false)

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
  // Move one or more entries into a chosen directory. Uses the rename route,
  // which takes two absolute paths and creates the missing parent — a
  // different directory in the destination is a move, not a rename.
  const performMove = async (entries: FileEntry[], destDir: string) => {
    const pathAtStart = currentPathRef.current
    let moved = 0
    const failed: string[] = []
    for (const entry of entries) {
      const from = entryPath(entry)
      const to = pathJoin(destDir, entry.name)
      if (from === to) continue
      try {
        await api.renamePath(from, to)
        moved += 1
      } catch (err: unknown) {
        const status = (err as { status?: number })?.status
        failed.push(
          status === 409
            ? t('files.moveExists', { name: entry.name, defaultValue: '{{name}} already exists there' })
            : `${entry.name}: ${err instanceof Error ? err.message : ''}`,
        )
      }
    }
    if (moved > 0) toast.success(t('files.moveSuccess', { count: moved, defaultValue: 'Moved {{count}}' }))
    // Name what failed. "Moved 37, 3 failed" without saying which three leaves
    // the operator to find them by hand.
    for (const message of failed.slice(0, 5)) toast.error(message)
    if (failed.length > 5) {
      toast.error(t('files.andMoreFailed', { count: failed.length - 5, defaultValue: 'and {{count}} more failed' }))
    }
    setSelectedPaths(new Set())
    if (searchActive) await handleSearch()
    else if (currentPathRef.current === pathAtStart) await fetchFiles()
  }

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

  // Every navigation goes through here, so the URL and the state move
  // together. Pushing a history entry is the point: it is what makes Back step
  // up a directory instead of leaving the page.
  const navigateTo = (path: string) => {
    setCurrentPath(path)
    setSearchParams(path === '/' ? {} : { path }, { replace: false })
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
    // Route by what the file IS, before anything else.
    //
    // Every non-directory used to land in Monaco. A PNG or a database came
    // back as a string of replacement characters, rendered as mojibake with
    // Save enabled — and saving wrote those characters over the original. The
    // listing's kind is derived from the name, so it is a hint; the read
    // endpoint sniffs the bytes and refuses a non-text file outright, which
    // catches an extensionless binary this branch would have missed.
    if (entry.kind === 'image' || entry.kind === 'binary') {
      setPreviewTarget({
        path: pathJoin(currentPath, entry.name),
        name: entry.name,
        size: entry.size,
        kind: entry.kind,
      })
      return
    }
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

  // Fetch a file and hand it to the browser as a download. Split out from
  // handleDownload so callers that already hold an absolute path — the preview
  // dialog, search results — do not have to fabricate a FileEntry.
  const downloadByPath = async (filePath: string, name: string) => {
    try {
      const blob = await api.downloadFile(filePath)
      downloadBlob(blob, name)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('files.downloadFailed'))
    }
  }

  // Download file
  const handleDownload = async (entry: FileEntry) => {
    // Prefer the entry's own absolute path. Re-joining onto currentPath is
    // wrong for anything that did not come from the current directory —
    // search results carry a path from wherever they were found.
    await downloadByPath(entry.path || pathJoin(currentPath, entry.name), entry.name)
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
      // createOnly, or typing the name of an existing file EMPTIES it — the
      // dialog says create and the effect was erase, with no warning and no
      // way back.
      await api.writeFile(pathJoin(pathAtStart, name.trim()), '', { createOnly: true })
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

  // Upload a batch, whether it arrived through the picker or a drop.
  //
  // The abort controller is what makes the Cancel button real: before it, the
  // X on the progress dialog did nothing and the only way to stop an upload
  // was to reload the page.
  const uploadFiles = async (list: File[]) => {
    if (list.length === 0) return
    const controller = new AbortController()
    uploadAbortRef.current = controller
    setUploading(true)
    const pathAtStart = currentPathRef.current
    try {
      for (const [index, file] of list.entries()) {
        setUploadProgress({ fileName: file.name, percent: 0, index: index + 1, total: list.length })
        try {
          await api.uploadFile(pathAtStart, file, (percent) => {
            setUploadProgress({ fileName: file.name, percent, index: index + 1, total: list.length })
          }, { signal: controller.signal })
          toast.success(t('files.uploadSuccess', { name: file.name }))
        } catch (err: unknown) {
          if (controller.signal.aborted) break
          const status = (err as { status?: number })?.status
          if (status === 409) {
            // The server refuses to clobber unless asked. Ask.
            const replace = await confirm({
              title: t('files.uploadExistsTitle', { defaultValue: 'Replace existing file?' }),
              description: t('files.uploadExists', { name: file.name, defaultValue: '{{name}} already exists in this folder.' }),
              confirmLabel: t('files.replace', { defaultValue: 'Replace' }),
              danger: true,
            })
            if (replace) {
              await api.uploadFile(pathAtStart, file, (percent) => {
                setUploadProgress({ fileName: file.name, percent, index: index + 1, total: list.length })
              }, { overwrite: true, signal: controller.signal })
              toast.success(t('files.uploadSuccess', { name: file.name }))
            }
            continue
          }
          toast.error(`${file.name}: ${err instanceof Error ? err.message : t('files.uploadFailed')}`)
        }
      }
    } finally {
      uploadAbortRef.current = null
      setUploadProgress(null)
      setUploading(false)
      if (fileInputRef.current) fileInputRef.current.value = ''
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    }
  }

  const handleFileSelected = async (e: React.ChangeEvent<HTMLInputElement>) => {
    const selectedFiles = e.target.files
    if (!selectedFiles || selectedFiles.length === 0) return
    await uploadFiles(Array.from(selectedFiles))
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

  // Ordering and the dotfile filter, both remembered per browser.
  const { sort, toggleSort, showHidden, toggleHidden } = useFileView()
  const sortedFiles = useVisibleEntries(files, sort, showHidden)

  // Search results get the same ordering but keep every hit: a search for
  // ".env" that then hid its own results would be indefensible.
  const sortedSearchResults = useMemo(() => sortEntries(searchResults, sort), [searchResults, sort])

  // Rows currently shown: search results when searching, otherwise the directory listing.
  const displayedFiles = searchActive ? sortedSearchResults : sortedFiles

  // How many rows the dotfile filter is holding back, so the count is honest
  // about what it is not showing.
  const hiddenCount = useMemo(
    () => (showHidden ? 0 : files.filter((e) => e.name.startsWith('.')).length),
    [files, showHidden],
  )

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

  // A real button, so the column is reachable by keyboard and announced as
  // sortable rather than being a bare clickable heading.
  const SortHeader = ({ column, label }: { column: SortKey; label: string }) => {
    const active = sort.key === column
    return (
      <button
        type="button"
        onClick={() => toggleSort(column)}
        aria-sort={active ? (sort.direction === 'asc' ? 'ascending' : 'descending') : 'none'}
        className="inline-flex items-center gap-1 rounded-sm outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/40"
      >
        {label}
        {active && (sort.direction === 'asc'
          ? <ArrowUp className="h-3 w-3" aria-hidden="true" />
          : <ArrowDown className="h-3 w-3" aria-hidden="true" />)}
      </button>
    )
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
      key: 'move',
      Icon: FolderInput,
      show: true,
      label: t('files.move', { defaultValue: 'Move' }),
      menuLabel: t('files.moveTo', { defaultValue: 'Move to…' }),
      onClick: () => setMoveTargets([entry]),
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
    <div
      className="relative space-y-4"
      onDragEnter={(e) => {
        // Only react to an actual file drag. Dragging text or a link inside
        // the page would otherwise arm an upload overlay for nothing.
        if (!e.dataTransfer?.types?.includes('Files')) return
        e.preventDefault()
        dragDepth.current += 1
        setDragActive(true)
      }}
      onDragOver={(e) => {
        if (!e.dataTransfer?.types?.includes('Files')) return
        // Without preventDefault the browser navigates away to the dropped
        // file — the default action for a drop is "open this".
        e.preventDefault()
        e.dataTransfer.dropEffect = 'copy'
      }}
      onDragLeave={() => {
        dragDepth.current = Math.max(0, dragDepth.current - 1)
        if (dragDepth.current === 0) setDragActive(false)
      }}
      onDrop={(e) => {
        if (!e.dataTransfer?.types?.includes('Files')) return
        e.preventDefault()
        dragDepth.current = 0
        setDragActive(false)
        if (searchActive) {
          // There is no single directory to drop into while showing results
          // gathered from all over the tree.
          toast.error(t('files.dropDuringSearch', { defaultValue: 'Leave search to upload here' }))
          return
        }
        void uploadFiles(Array.from(e.dataTransfer.files))
      }}
    >
      {dragActive && (
        <div className="pointer-events-none absolute inset-0 z-20 flex items-center justify-center rounded-2xl border-2 border-dashed border-primary bg-primary/5">
          <div className="flex items-center gap-2 rounded-xl bg-card px-4 py-2 card-shadow">
            <Upload className="h-4 w-4 text-primary" aria-hidden="true" />
            <span className="text-[13px] font-medium">
              {t('files.dropHere', { path: currentPath, defaultValue: 'Drop to upload into {{path}}' })}
            </span>
          </div>
        </div>
      )}
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

      {/* Toolbar.
          Wraps, and the search box is fluid below sm. It used to be a single
          non-wrapping row holding a fixed 224px input and four buttons, which
          pushed Upload off the right edge of a 390px screen entirely — the
          control was not small, it was unreachable. */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex flex-1 items-center gap-2 min-w-0">
          <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary shrink-0">
            {searchActive
              ? t('files.searchResultCount', { count: searchCount })
              : t('files.count', { count: displayedFiles.length })}
          </span>
          {hiddenCount > 0 && (
            <span className="hidden text-[11px] text-muted-foreground sm:inline">
              {t('files.hiddenCount', { count: hiddenCount, defaultValue: '{{count}} hidden' })}
            </span>
          )}
          <div className="relative min-w-0 flex-1 sm:flex-none">
            <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground pointer-events-none" />
            <Input
              value={searchQuery}
              onChange={(e) => setSearchQuery(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleSearch()
                else if (e.key === 'Escape' && searchActive) exitSearch()
              }}
              placeholder={t('files.searchPlaceholder')}
              className="h-8 w-full pl-8 pr-8 text-sm sm:w-56"
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
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant="outline"
            size="sm"
            className="rounded-xl"
            onClick={toggleHidden}
            title={showHidden ? t('files.hideHidden', { defaultValue: 'Hide dotfiles' }) : t('files.showHidden', { defaultValue: 'Show dotfiles' })}
            aria-pressed={showHidden}
          >
            {showHidden ? <Eye /> : <EyeOff />}
            <span className="hidden sm:inline">
              {showHidden ? t('files.hideHidden', { defaultValue: 'Hide dotfiles' }) : t('files.showHidden', { defaultValue: 'Show dotfiles' })}
            </span>
          </Button>
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
          <div className="flex flex-wrap items-center gap-2">
            <Button variant="ghost" size="sm" className="rounded-xl" onClick={() => setSelectedPaths(new Set())}>
              {t('common.cancel')}
            </Button>
            {/* Selecting rows used to lead to exactly one action. Deleting is
                rarely the only thing you want done to forty files. */}
            <Button
              variant="outline"
              size="sm"
              className="rounded-xl"
              onClick={() => setMoveTargets(displayedFiles.filter((e) => selectedPaths.has(entryPath(e))))}
              disabled={bulkDeleting}
            >
              <FolderInput />
              {t('files.moveTo', { defaultValue: 'Move to…' })}
            </Button>
            <Button variant="destructive" size="sm" className="rounded-xl" onClick={handleBulkDelete} disabled={bulkDeleting}>
              {bulkDeleting ? <Loader2 className="animate-spin" /> : <Trash2 />}
              {t('files.deleteSelected')}
            </Button>
          </div>
        </div>
      )}

      {/* Phone view. The table below is six columns wide and simply does not
          fit a 390px screen — size, modified and permissions were off-screen
          entirely rather than merely cramped. */}
      <FileCardList
        entries={displayedFiles}
        loading={loading || searchLoading}
        emptyMessage={searchActive ? t('files.searchEmpty') : t('files.empty')}
        selectedPaths={selectedPaths}
        entryPath={entryPath}
        onToggleSelect={toggleSelected}
        onOpen={openEntry}
        actionsFor={entryActions}
        searchActive={searchActive}
      />

      {/* File listing table */}
      <div className="hidden bg-card rounded-2xl card-shadow overflow-hidden md:block">
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
            <TableHead><SortHeader column="name" label={t('files.name')} /></TableHead>
            <TableHead className="w-28"><SortHeader column="size" label={t('files.size')} /></TableHead>
            <TableHead className="w-44"><SortHeader column="modTime" label={t('files.modified')} /></TableHead>
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
      <FileEditorDialog
        target={editorTarget}
        onOpenChange={(open) => { if (!open) setEditorTarget(null) }}
        onSaved={fetchFiles}
      />

      <FolderPickerDialog
        open={!!moveTargets}
        title={t('files.moveTitle', { defaultValue: 'Move to folder' })}
        description={
          moveTargets && moveTargets.length === 1
            ? entryPath(moveTargets[0])
            : t('files.moveCount', { count: moveTargets?.length ?? 0, defaultValue: '{{count}} items' })
        }
        initialPath={currentPath}
        confirmLabel={t('files.move', { defaultValue: 'Move' })}
        onOpenChange={(open) => { if (!open) setMoveTargets(null) }}
        onConfirm={(dest) => {
          const targets = moveTargets ?? []
          setMoveTargets(null)
          void performMove(targets, dest)
        }}
      />

      <FilePreviewDialog
        target={previewTarget}
        onOpenChange={(open) => { if (!open) setPreviewTarget(null) }}
        onDownload={(target) => {
          void downloadByPath(target.path, target.name)
          setPreviewTarget(null)
        }}
      />

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
              {uploadProgress && uploadProgress.total > 1
                ? t('files.uploadingNth', {
                    index: uploadProgress.index,
                    total: uploadProgress.total,
                    percent: uploadProgress.percent,
                    defaultValue: '{{index}} of {{total}} · {{percent}}%',
                  })
                : `${uploadProgress?.percent ?? 0}%`}
            </p>
          </div>
          <DialogFooter>
            {/* A real cancel. The dialog previously had no way out at all —
                an upload could only be stopped by reloading the page. */}
            <Button
              variant="outline"
              className="rounded-xl"
              onClick={() => uploadAbortRef.current?.abort()}
            >
              {t('common.cancel')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
