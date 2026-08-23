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
  LayoutGrid,
  List as ListIcon,
  FolderInput,
  Server,
  MoreHorizontal,
  Lock,
  FileArchive,
  PackageOpen,
  Eye,
  EyeOff,
} from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { usePrompt } from '@/components/PromptDialog'
import { TypeToConfirmDialog } from '@/components/TypeToConfirmDialog'
import { formatBytes, formatDate, pathJoin, downloadBlob, cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Checkbox } from '@/components/ui/checkbox'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
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
import { FileGrid } from './files/components/FileGrid'
import { dispatchContextMenu, useLongPress } from './files/useLongPress'
import { useVirtualRows, VIRTUALIZE_THRESHOLD } from './files/useVirtualRows'
import { FileCardList } from './files/components/FileCardList'
import { FolderPickerDialog } from './files/components/FolderPickerDialog'
import { PermissionsDialog } from './files/components/PermissionsDialog'
import { TrashDialog } from './files/components/TrashDialog'
import type { EntryAction } from './files/entryActions'

import type { FileEntry } from '@/types/api'


// The actions that stay visible in a desktop row. Everything else moves into
// the overflow menu — eight icons in a line is a target-picking problem, not a
// toolbar.
const PRIMARY_ACTIONS = new Set(['edit', 'download', 'delete'])

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
  const [permissionsTarget, setPermissionsTarget] = useState<FileEntry | null>(null)
  const [trashOpen, setTrashOpen] = useState(false)
  // The background menu's long-press target. On touch there is no right-click,
  // so without this the "new file / new folder / upload" menu is unreachable.
  const backgroundRef = useRef<HTMLDivElement>(null)
  const backgroundLongPress = useLongPress((x, y) => dispatchContextMenu(backgroundRef.current, x, y))

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
  // Which machine's filesystem this is. In a cluster the same page targets a
  // different host depending on the node picker, and a path like /etc looks
  // identical on every one of them — deleting the right file on the wrong
  // machine is a mistake the UI was doing nothing to prevent.
  const [nodeName, setNodeName] = useState<string | null>(null)

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
  const [bulkProgress, setBulkProgress] = useState<{ current: string; index: number; total: number } | null>(null)
  const [bulkDeleteRequest, setBulkDeleteRequest] = useState<{ paths: string[]; dirCount: number } | null>(null)
  // A flag rather than an AbortController: the requests already in flight
  // should finish rather than leave a half-deleted directory, so cancelling
  // means "stop starting new ones".
  const bulkCancelRef = useRef(false)

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

  // Pack a selection. The name is suggested rather than demanded — a single
  // folder becomes folder.tar.gz, a mixed selection takes the directory's name.
  const handleArchive = async (entries: FileEntry[]) => {
    if (entries.length === 0) return
    const base = entries.length === 1
      ? entries[0].name
      : (currentPath.split('/').filter(Boolean).pop() || 'archive')
    const name = await prompt({
      title: t('files.archiveTitle', { defaultValue: 'Create archive' }),
      description: t('files.archiveDescription', { count: entries.length, defaultValue: 'Packing {{count}} entries' }),
      defaultValue: `${base}.tar.gz`,
      confirmLabel: t('files.createArchive', { defaultValue: 'Create' }),
    })
    const trimmed = name?.trim()
    if (!trimmed) return
    const format = trimmed.toLowerCase().endsWith('.zip') ? 'zip' : 'tar.gz'
    const pathAtStart = currentPathRef.current
    try {
      await api.createArchive(entries.map((e) => entryPath(e)), pathJoin(pathAtStart, trimmed), format)
      toast.success(t('files.archiveCreated', { name: trimmed, defaultValue: '{{name}} created' }))
      setSelectedPaths(new Set())
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      const status = (err as { status?: number })?.status
      toast.error(status === 409
        ? t('files.archiveExists', { name: trimmed, defaultValue: '{{name}} already exists' })
        : (err instanceof Error ? err.message : t('files.archiveFailed', { defaultValue: 'Could not create the archive' })))
    }
  }

  // Unpack into a folder of the archive's own name, so extracting into a busy
  // directory does not scatter a hundred files through it.
  const handleExtract = async (entry: FileEntry) => {
    const stem = entry.name.replace(/\.(tar\.gz|tgz|tar|zip)$/i, '')
    const dest = await prompt({
      title: t('files.extractTitle', { defaultValue: 'Extract archive' }),
      description: entry.path,
      defaultValue: pathJoin(currentPath, stem),
      confirmLabel: t('files.extract', { defaultValue: 'Extract' }),
    })
    const trimmed = dest?.trim()
    if (!trimmed) return
    const pathAtStart = currentPathRef.current
    try {
      const result = await api.extractArchive(entryPath(entry), trimmed)
      toast.success(t('files.extracted', { count: result.entries, defaultValue: 'Extracted {{count}} entries' }))
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('files.extractFailed', { defaultValue: 'Could not extract the archive' }))
    }
  }

  const isArchiveName = (name: string) => /\.(tar\.gz|tgz|tar|zip)$/i.test(name)

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

  // Delete a selection, reporting as it goes.
  //
  // This used to be a bare loop behind one spinner: forty serial DELETEs with
  // no indication of where it had got to, no way to stop it, and a closing
  // toast reading "Deleted 37, 3 failed" that named none of the three — the
  // operator was left to find them by eye. Worse, the Cancel button beside it
  // only cleared the selection, so pressing it looked like a stop while files
  // kept disappearing.
  const runBulkDelete = async (paths: string[]) => {
    setBulkDeleting(true)
    bulkCancelRef.current = false
    const failures: string[] = []
    let done = 0
    for (const [index, p] of paths.entries()) {
      if (bulkCancelRef.current) break
      setBulkProgress({ current: p.split('/').pop() || p, index: index + 1, total: paths.length })
      try {
        await api.deletePath(p)
        done += 1
      } catch (err: unknown) {
        failures.push(`${p}: ${err instanceof Error ? err.message : ''}`)
      }
    }
    const cancelled = bulkCancelRef.current
    setBulkProgress(null)
    setBulkDeleting(false)
    setSelectedPaths(new Set())

    if (done > 0) toast.success(t('files.bulkDeleted', { count: done, defaultValue: 'Deleted {{count}}' }))
    if (cancelled) {
      // Say what actually happened. A cancel part-way through is not "nothing
      // happened", and pretending otherwise leaves the operator with a wrong
      // model of their own filesystem.
      toast.info(t('files.bulkCancelled', {
        done,
        remaining: paths.length - done - failures.length,
        defaultValue: 'Stopped after {{done}} — {{remaining}} left untouched',
      }))
    }
    for (const message of failures.slice(0, 5)) toast.error(message)
    if (failures.length > 5) {
      toast.error(t('files.andMoreFailed', { count: failures.length - 5 }))
    }

    if (searchActive) await handleSearch()
    else await fetchFiles()
  }

  // Above this many entries the confirmation asks for the count to be typed.
  // Small selections stay one click; a bulk delete of a whole directory should
  // cost a deliberate keystroke.
  const typeToConfirmThreshold = 5

  const handleBulkDelete = async () => {
    const paths = Array.from(selectedPaths)
    if (paths.length === 0) return

    const entries = displayedFiles.filter((e) => selectedPaths.has(entryPath(e)))
    const dirCount = entries.filter((e) => e.isDir).length

    if (paths.length > typeToConfirmThreshold) {
      setBulkDeleteRequest({ paths, dirCount })
      return
    }

    // Name what is going, rather than only counting it. "Delete 4 items?" tells
    // the operator nothing about whether they picked the right four.
    const names = entries.map((e) => e.name)
    const ok = await confirm({
      title: t('files.deleteSelected'),
      description: (
        <span>
          {t('files.deleteSelectedConfirm', { count: paths.length })}
          <span className="mt-2 block font-mono text-[12px] text-muted-foreground">
            {names.join(', ')}
          </span>
          {dirCount > 0 && (
            <span className="mt-2 block text-warning">
              {t('files.deleteDirWarning', {
                count: dirCount,
                defaultValue: '{{count}} of these are folders — everything inside goes too.',
              })}
            </span>
          )}
        </span>
      ),
      danger: true,
    })
    if (!ok) return
    await runBulkDelete(paths)
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
      // A default rather than an empty box. Typing "memo" produced a file with
      // no extension, which then opened as plain text anyway but told the
      // operator nothing about what it was.
      defaultValue: 'untitled.txt',
      confirmLabel: t('files.createFile'),
    })
    if (!name?.trim()) return
    let fileName = name.trim()
    // A name with no dot gets .txt. Dotfiles are left alone — ".env" is a
    // complete name, not an extensionless one.
    if (!fileName.startsWith('.') && !fileName.includes('.')) fileName += '.txt'
    const pathAtStart = currentPathRef.current
    try {
      // createOnly, or typing the name of an existing file EMPTIES it — the
      // dialog says create and the effect was erase, with no warning and no
      // way back.
      const filePath = pathJoin(pathAtStart, fileName)
      await api.writeFile(filePath, '', { createOnly: true })
      toast.success(t('files.fileCreated'))
      if (currentPathRef.current === pathAtStart) await fetchFiles()
      // Open it. Creating an empty file and stopping meant finding it again in
      // the listing before a single character could be typed into it.
      setEditorTarget({ path: filePath, name: fileName })
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

  useEffect(() => {
    let cancelled = false
    // Only meaningful in a cluster; a standalone panel has one filesystem and
    // naming it would be noise.
    if (!api.currentNode) {
      setNodeName(null)
      return
    }
    api.getClusterNodes(true)
      .then((data) => {
        if (cancelled) return
        const node = data?.nodes?.find((n) => n.id === api.currentNode)
        // Fall back to the id: a name we could not resolve is still better
        // than no indication of which machine this is.
        setNodeName(node?.name || api.currentNode)
      })
      .catch(() => { if (!cancelled) setNodeName(api.currentNode) })
    return () => { cancelled = true }
  }, [])

  // Ordering and the dotfile filter, both remembered per browser.
  const { sort, toggleSort, showHidden, toggleHidden, mode, setMode } = useFileView()
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

  // Arrow-key navigation over the listing.
  //
  // Every row's name was already a real button, so Tab reached them — but Tab
  // walks every control in every row, which on a directory of two hundred
  // files means a hundred presses to reach the next name. Arrow keys move by
  // row, Enter opens, Space selects. Bound on the page rather than per-row so
  // it works before anything inside the list has focus.
  const listRef = useRef<HTMLDivElement>(null)
  const [activeIndex, setActiveIndex] = useState(-1)

  // A row that no longer exists must not stay "active" — deleting the last
  // entry would otherwise leave the cursor pointing past the end.
  useEffect(() => {
    setActiveIndex((prev) => (prev >= displayedFiles.length ? displayedFiles.length - 1 : prev))
  }, [displayedFiles.length])

  const handleListKeyDown = (e: React.KeyboardEvent) => {
    // Never steal keys from a field: the path editor and the search box both
    // live on this page.
    const target = e.target as HTMLElement
    if (target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.isContentEditable) return
    if (displayedFiles.length === 0) return

    switch (e.key) {
      case 'ArrowDown':
        e.preventDefault()
        setActiveIndex((prev) => Math.min(prev + 1, displayedFiles.length - 1))
        break
      case 'ArrowUp':
        e.preventDefault()
        setActiveIndex((prev) => Math.max(prev - 1, 0))
        break
      case 'Home':
        e.preventDefault()
        setActiveIndex(0)
        break
      case 'End':
        e.preventDefault()
        setActiveIndex(displayedFiles.length - 1)
        break
      case 'Enter':
        if (activeIndex >= 0) {
          e.preventDefault()
          openEntry(displayedFiles[activeIndex])
        }
        break
      case ' ':
        if (activeIndex >= 0) {
          e.preventDefault()
          toggleSelected(entryPath(displayedFiles[activeIndex]))
        }
        break
      case 'Backspace':
        // Up a directory, the way a file manager is expected to behave.
        if (!searchActive && currentPath !== '/') {
          e.preventDefault()
          navigateToSegment(pathSegments.length - 2)
        }
        break
      default:
        break
    }
  }

  // Keep the active row on screen when the arrows walk past the fold.
  useEffect(() => {
    if (activeIndex < 0) return
    listRef.current
      ?.querySelector(`[data-row-index="${activeIndex}"]`)
      ?.scrollIntoView({ block: 'nearest' })
  }, [activeIndex])

  // Only window the list when it is big enough to be worth it. Windowing costs
  // a scroll container and breaks find-in-page for rows that are not rendered,
  // which is a bad trade on the tens-of-entries directory people actually open.
  const virtualized = displayedFiles.length > VIRTUALIZE_THRESHOLD
  const rows = useVirtualRows({
    count: displayedFiles.length,
    rowHeight: 45,
    enabled: virtualized,
  })
  const windowedFiles = virtualized ? displayedFiles.slice(rows.start, rows.end) : displayedFiles

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
      key: 'extract',
      Icon: PackageOpen,
      show: !entry.isDir && isArchiveName(entry.name),
      label: t('files.extract', { defaultValue: 'Extract' }),
      menuLabel: t('files.extractTitle', { defaultValue: 'Extract archive' }),
      onClick: () => void handleExtract(entry),
    },
    {
      key: 'archive',
      Icon: FileArchive,
      show: true,
      label: t('files.archive', { defaultValue: 'Archive' }),
      menuLabel: t('files.archiveTitle', { defaultValue: 'Create archive' }),
      onClick: () => void handleArchive([entry]),
    },
    {
      key: 'permissions',
      Icon: Lock,
      show: !searchActive,
      label: t('files.permissions'),
      menuLabel: t('files.permissions'),
      onClick: () => setPermissionsTarget(entry),
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
      ref={listRef}
      className="relative space-y-4 outline-none"
      tabIndex={-1}
      onKeyDown={handleListKeyDown}
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
          className="-mx-2 flex items-center gap-1 overflow-x-auto rounded-md border border-transparent px-2 py-1.5 text-sm text-muted-foreground transition-colors cursor-text hover:border-border"
          onClick={handlePathEditStart}
        >
          {/* Name the machine before the path. In a cluster /etc looks the
              same on every host, and the sidebar that would otherwise say
              which one is not there on a phone. */}
          {nodeName && (
            <span
              className="mr-1 inline-flex shrink-0 items-center gap-1 rounded-md bg-warning/10 px-1.5 py-0.5 font-mono text-[11px] text-warning"
              title={t('files.browsingNode', { node: nodeName, defaultValue: 'Browsing {{node}}' })}
            >
              <Server className="h-3 w-3" aria-hidden="true" />
              {nodeName}
            </span>
          )}
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
          {/* List stays the default: it shows size, date, owner and permissions
              at a glance and fits more rows. The grid is for looking at images,
              which is the one thing the list cannot do. */}
          <div className="flex items-center rounded-xl border p-0.5">
            <Button
              variant={mode === 'list' ? 'secondary' : 'ghost'}
              size="icon-xs"
              className="rounded-lg"
              onClick={() => setMode('list')}
              aria-pressed={mode === 'list'}
              title={t('files.viewList', { defaultValue: 'List' })}
              aria-label={t('files.viewList', { defaultValue: 'List' })}
            >
              <ListIcon />
            </Button>
            <Button
              variant={mode === 'grid' ? 'secondary' : 'ghost'}
              size="icon-xs"
              className="rounded-lg"
              onClick={() => setMode('grid')}
              aria-pressed={mode === 'grid'}
              title={t('files.viewGrid', { defaultValue: 'Grid' })}
              aria-label={t('files.viewGrid', { defaultValue: 'Grid' })}
            >
              <LayoutGrid />
            </Button>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="rounded-xl"
            onClick={() => setTrashOpen(true)}
            title={t('files.trash', { defaultValue: 'Trash' })}
          >
            <Trash2 />
            <span className="hidden sm:inline">{t('files.trash', { defaultValue: 'Trash' })}</span>
          </Button>
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
            <Button
              variant="outline"
              size="sm"
              className="rounded-xl"
              onClick={() => void handleArchive(displayedFiles.filter((e) => selectedPaths.has(entryPath(e))))}
              disabled={bulkDeleting}
            >
              <FileArchive />
              {t('files.archive', { defaultValue: 'Archive' })}
            </Button>
            <Button variant="destructive" size="sm" className="rounded-xl" onClick={handleBulkDelete} disabled={bulkDeleting}>
              {bulkDeleting ? <Loader2 className="animate-spin" /> : <Trash2 />}
              {t('files.deleteSelected')}
            </Button>
          </div>
        </div>
      )}

      {/* The whole listing area is the background context target.
          It used to be a 40-pixel strip below the table, so right-clicking
          "empty space" meant finding a thin band by eye — the menu was there
          all along and effectively undiscoverable. A row or tile still wins:
          nested triggers resolve to the innermost one. */}
      <ContextMenu>
        <ContextMenuTrigger asChild>
          <div
            className="min-h-[240px]"
            ref={backgroundRef}
            {...backgroundLongPress}
          >
      {mode === 'grid' ? (
        <FileGrid
          entries={displayedFiles}
          loading={loading || searchLoading}
          emptyMessage={searchActive ? t('files.searchEmpty') : t('files.empty')}
          selectedPaths={selectedPaths}
          entryPath={entryPath}
          onToggleSelect={toggleSelected}
          onOpen={openEntry}
          actionsFor={entryActions}
          activeIndex={activeIndex}
        />
      ) : (
        <>
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
      <div
        ref={rows.scrollRef}
        className={cn(
          'hidden rounded-2xl bg-card card-shadow md:block',
          virtualized ? 'max-h-[calc(100vh-20rem)] overflow-auto' : 'overflow-hidden',
        )}
      >
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
            <TableHead className="w-32">{t('files.owner', { defaultValue: 'Owner' })}</TableHead>
            <TableHead className="w-28">{t('files.permissions')}</TableHead>
            <TableHead className="text-right w-36">{t('common.actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {displayedFiles.length === 0 && !loading && !searchLoading && (
            <TableRow>
              <TableCell colSpan={7} className="text-center text-muted-foreground py-8">
                {searchActive ? t('files.searchEmpty') : t('files.empty')}
              </TableCell>
            </TableRow>
          )}
          {loading && files.length === 0 && (
            <TableRow>
              <TableCell colSpan={7} className="text-center py-8">
                <div className="flex items-center justify-center gap-2 text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t('files.loading')}
                </div>
              </TableCell>
            </TableRow>
          )}
          {virtualized && rows.padTop > 0 && (
            <TableRow aria-hidden="true"><TableCell colSpan={7} style={{ height: rows.padTop, padding: 0 }} /></TableRow>
          )}
          {windowedFiles.map((entry) => {
            const rowPath = entryPath(entry)
            const actions = entryActions(entry)
            return (
            <ContextMenu key={rowPath}>
              <ContextMenuTrigger asChild>
                <TableRow
                  data-row-index={displayedFiles.indexOf(entry)}
                  aria-selected={selectedPaths.has(rowPath)}
                  className={cn(
                    'cursor-pointer hover:bg-secondary/50',
                    displayedFiles.indexOf(entry) === activeIndex && 'bg-secondary/70 ring-1 ring-inset ring-ring/40',
                  )}
                  onClick={() => { setActiveIndex(displayedFiles.indexOf(entry)); openEntry(entry) }}
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
                        {entry.linkTarget && (
                          <span className="block truncate font-mono text-[11px] text-muted-foreground">
                            → {entry.linkTarget}
                          </span>
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
                  <TableCell className="truncate font-mono text-xs text-muted-foreground">
                    {entry.owner
                      ? `${entry.owner.user || entry.owner.uid}:${entry.owner.group || entry.owner.gid}`
                      : '-'}
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs font-mono">
                    {entry.mode || '-'}
                  </TableCell>
                  <TableCell className="text-right" onClick={(e) => e.stopPropagation()}>
                    {/* The three actions people reach for stay as buttons; the
                        rest live behind one menu. Adding archive, extract and
                        permissions took the row to eight 24-pixel targets in a
                        line — the same mistake the phone layout had just
                        fixed, with Delete sitting next to Rename. */}
                    <div className="flex items-center justify-end gap-1">
                      {actions.filter((a) => a.show && PRIMARY_ACTIONS.has(a.key)).map((a) => (
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
                      {actions.some((a) => a.show && !PRIMARY_ACTIONS.has(a.key)) && (
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button variant="ghost" size="icon-xs" aria-label={t('common.actions')}>
                              <MoreHorizontal />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            {actions.filter((a) => a.show && !PRIMARY_ACTIONS.has(a.key)).map((a) => (
                              <DropdownMenuItem
                                key={a.key}
                                variant={a.destructive ? 'destructive' : undefined}
                                onClick={a.onClick}
                              >
                                <a.Icon className="h-4 w-4" />
                                {a.menuLabel}
                              </DropdownMenuItem>
                            ))}
                          </DropdownMenuContent>
                        </DropdownMenu>
                      )}
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
          {virtualized && rows.padBottom > 0 && (
            <TableRow aria-hidden="true"><TableCell colSpan={7} style={{ height: rows.padBottom, padding: 0 }} /></TableRow>
          )}
        </TableBody>
      </Table>
      </div>

        </>
      )}
          </div>
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

      {/* Bulk delete progress. The Cancel here actually stops the run — the
          button that sat beside the Delete action only cleared the selection,
          so pressing it looked like a stop while files kept disappearing. */}
      <Dialog open={!!bulkProgress} onOpenChange={() => {}}>
        <DialogContent className="sm:max-w-md" onPointerDownOutside={(e) => e.preventDefault()}>
          <DialogHeader>
            <DialogTitle>{t('files.deleting', { defaultValue: 'Deleting' })}</DialogTitle>
            <DialogDescription className="truncate font-mono">
              {bulkProgress?.current}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-3 py-2">
            <div className="h-1.5 overflow-hidden rounded-full bg-secondary">
              <div
                className="h-full rounded-full transition-all duration-200"
                style={{
                  width: `${bulkProgress ? Math.round((bulkProgress.index / bulkProgress.total) * 100) : 0}%`,
                  backgroundColor: 'var(--destructive)',
                }}
              />
            </div>
            <p className="text-center text-[13px] text-muted-foreground">
              {t('files.deletingNth', {
                index: bulkProgress?.index ?? 0,
                total: bulkProgress?.total ?? 0,
                defaultValue: '{{index}} of {{total}}',
              })}
            </p>
          </div>
          <DialogFooter>
            <Button
              variant="outline"
              className="rounded-xl"
              onClick={() => { bulkCancelRef.current = true }}
            >
              {t('common.cancel')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Past a handful of entries, deleting costs a typed confirmation. */}
      <TypeToConfirmDialog
        open={!!bulkDeleteRequest}
        onOpenChange={(open) => { if (!open) setBulkDeleteRequest(null) }}
        title={t('files.deleteSelected')}
        description={t('files.deleteManyWarning', {
          count: bulkDeleteRequest?.paths.length ?? 0,
          dirs: bulkDeleteRequest?.dirCount ?? 0,
          defaultValue:
            'About to delete {{count}} entries, {{dirs}} of them folders. Everything inside a folder goes with it, and none of this can be undone.',
        })}
        confirmPhrase={String(bulkDeleteRequest?.paths.length ?? '')}
        confirmLabel={t('files.deleteSelected')}
        loading={bulkDeleting}
        onConfirm={() => {
          const request = bulkDeleteRequest
          setBulkDeleteRequest(null)
          if (request) void runBulkDelete(request.paths)
        }}
      />

      <TrashDialog
        open={trashOpen}
        onOpenChange={setTrashOpen}
        onRestored={fetchFiles}
      />

      <PermissionsDialog
        entry={permissionsTarget}
        onOpenChange={(open) => { if (!open) setPermissionsTarget(null) }}
        onApplied={fetchFiles}
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
