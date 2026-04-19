import { useState, useEffect, useCallback, useRef, useMemo } from 'react'
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
  Save,
} from 'lucide-react'
import { toast } from 'sonner'
import Editor from '@monaco-editor/react'
import { api } from '@/lib/api'
import { formatBytes, formatDate } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
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

import type { FileEntry } from '@/types/api'


function getLanguageFromFilename(filename: string): string {
  const ext = filename.split('.').pop()?.toLowerCase() || ''
  const languageMap: Record<string, string> = {
    js: 'javascript',
    jsx: 'javascript',
    ts: 'typescript',
    tsx: 'typescript',
    py: 'python',
    rb: 'ruby',
    go: 'go',
    rs: 'rust',
    java: 'java',
    c: 'c',
    cpp: 'cpp',
    h: 'c',
    hpp: 'cpp',
    cs: 'csharp',
    php: 'php',
    html: 'html',
    htm: 'html',
    css: 'css',
    scss: 'scss',
    less: 'less',
    json: 'json',
    xml: 'xml',
    yaml: 'yaml',
    yml: 'yaml',
    toml: 'toml',
    ini: 'ini',
    conf: 'plaintext',
    cfg: 'ini',
    md: 'markdown',
    sql: 'sql',
    sh: 'shell',
    bash: 'shell',
    zsh: 'shell',
    dockerfile: 'dockerfile',
    makefile: 'plaintext',
    lua: 'lua',
    r: 'r',
    swift: 'swift',
    kt: 'kotlin',
    vue: 'html',
    svelte: 'html',
  }
  return languageMap[ext] || 'plaintext'
}

function joinPath(...parts: string[]): string {
  const [first, ...rest] = parts
  const joined = [first.replace(/\/+$/, ''), ...rest.map(p => p.replace(/^\/+/, ''))].join('/')
  return joined.replace(/\/+/g, '/') || '/'
}

export default function Files() {
  const { t } = useTranslation()
  const fileInputRef = useRef<HTMLInputElement>(null)

  // Core state
  const [currentPath, setCurrentPath] = useState('/')
  const [files, setFiles] = useState<FileEntry[]>([])
  const [loading, setLoading] = useState(true)
  const currentPathRef = useRef(currentPath)
  useEffect(() => { currentPathRef.current = currentPath }, [currentPath])

  // Edit dialog state
  const [editOpen, setEditOpen] = useState(false)
  const [editFilePath, setEditFilePath] = useState('')
  const [editFileName, setEditFileName] = useState('')
  const [editContent, setEditContent] = useState('')
  const [editLoading, setEditLoading] = useState(false)
  const [editSaving, setEditSaving] = useState(false)

  // New folder dialog state
  const [newFolderOpen, setNewFolderOpen] = useState(false)
  const [newFolderName, setNewFolderName] = useState('')
  const [newFolderCreating, setNewFolderCreating] = useState(false)

  // Delete confirmation dialog state
  const [deleteTarget, setDeleteTarget] = useState<FileEntry | null>(null)
  const [deleteLoading, setDeleteLoading] = useState(false)

  // Rename dialog state
  const [renameTarget, setRenameTarget] = useState<FileEntry | null>(null)
  const [renameNewName, setRenameNewName] = useState('')
  const [renameLoading, setRenameLoading] = useState(false)

  // New file dialog state
  const [newFileOpen, setNewFileOpen] = useState(false)
  const [newFileName, setNewFileName] = useState('')
  const [newFileCreating, setNewFileCreating] = useState(false)

  // Upload state
  const [uploading, setUploading] = useState(false)
  const [uploadProgress, setUploadProgress] = useState<{ fileName: string; percent: number } | null>(null)

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
    const newPath = joinPath(currentPath, entry.name)
    navigateTo(newPath)
  }

  const handleFileClick = (entry: FileEntry) => {
    handleEditFile(entry)
  }

  // Edit file
  const editAbortRef = useRef<AbortController | null>(null)
  const editMaxBytes = 5 * 1024 * 1024 // 5 MB; server also enforces similar cap
  const handleEditFile = async (entry: FileEntry) => {
    // Guard against opening huge files in Monaco — multi-MB text loads can
    // freeze the tab even though the fetch succeeds.
    if (entry.size > editMaxBytes) {
      const ok = window.confirm(
        t('files.largeFileWarning', {
          size: Math.round(entry.size / 1024 / 1024),
        }) ||
          `This file is ${Math.round(entry.size / 1024 / 1024)} MB and may freeze the editor. Open anyway?`,
      )
      if (!ok) return
    }
    editAbortRef.current?.abort()
    const controller = new AbortController()
    editAbortRef.current = controller
    const filePath = joinPath(currentPath, entry.name)
    setEditFilePath(filePath)
    setEditFileName(entry.name)
    setEditContent('')
    setEditOpen(true)
    setEditLoading(true)
    try {
      const data = await api.readFile(filePath)
      if (controller.signal.aborted) return
      setEditContent(data.content || '')
    } catch (err: unknown) {
      if (controller.signal.aborted) return
      const message = err instanceof Error ? err.message : t('files.readFailed')
      toast.error(message)
      setEditOpen(false)
    } finally {
      if (!controller.signal.aborted) setEditLoading(false)
    }
  }

  const handleSaveFile = async () => {
    setEditSaving(true)
    try {
      await api.writeFile(editFilePath, editContent)
      toast.success(t('files.saveSuccess'))
      setEditOpen(false)
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.saveFailed')
      toast.error(message)
    } finally {
      setEditSaving(false)
    }
  }

  // Download file
  const handleDownload = async (entry: FileEntry) => {
    const filePath = joinPath(currentPath, entry.name)
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
    if (!newFileName.trim()) return
    setNewFileCreating(true)
    const pathAtStart = currentPathRef.current
    try {
      const filePath = joinPath(pathAtStart, newFileName.trim())
      await api.writeFile(filePath, '')
      toast.success(t('files.fileCreated'))
      setNewFileOpen(false)
      setNewFileName('')
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.fileCreateFailed')
      toast.error(message)
    } finally {
      setNewFileCreating(false)
    }
  }

  // New folder
  const handleCreateFolder = async () => {
    if (!newFolderName.trim()) return
    setNewFolderCreating(true)
    const pathAtStart = currentPathRef.current
    try {
      const dirPath = joinPath(pathAtStart, newFolderName.trim())
      await api.createDir(dirPath)
      toast.success(t('files.folderCreated'))
      setNewFolderOpen(false)
      setNewFolderName('')
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.folderCreateFailed')
      toast.error(message)
    } finally {
      setNewFolderCreating(false)
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
  const handleDelete = async () => {
    if (!deleteTarget) return
    setDeleteLoading(true)
    const pathAtStart = currentPathRef.current
    try {
      const targetPath = joinPath(pathAtStart, deleteTarget.name)
      await api.deletePath(targetPath)
      toast.success(t('files.deleteSuccess', { name: deleteTarget.name }))
      setDeleteTarget(null)
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.deleteFailed')
      toast.error(message)
    } finally {
      setDeleteLoading(false)
    }
  }

  // Rename
  const handleRename = async () => {
    if (!renameTarget || !renameNewName.trim()) return
    setRenameLoading(true)
    const pathAtStart = currentPathRef.current
    try {
      const oldPath = joinPath(pathAtStart, renameTarget.name)
      const newPath = joinPath(pathAtStart, renameNewName.trim())
      await api.renamePath(oldPath, newPath)
      toast.success(t('files.renameSuccess'))
      setRenameTarget(null)
      setRenameNewName('')
      if (currentPathRef.current === pathAtStart) await fetchFiles()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('files.renameFailed')
      toast.error(message)
    } finally {
      setRenameLoading(false)
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
            className="flex items-center gap-1 hover:text-foreground transition-colors shrink-0"
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
                  index === pathSegments.length - 1
                    ? 'font-medium text-foreground'
                    : 'hover:text-foreground transition-colors'
                }
              >
                {segment}
              </button>
            </span>
          ))}
        </nav>
      )}

      {/* Toolbar */}
      <div className="flex items-center justify-between">
        <span className="inline-flex items-center px-3 py-1 rounded-full text-[13px] font-semibold bg-primary/10 text-primary">
          {t('files.count', { count: files.length })}
        </span>
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
            onClick={() => {
              setNewFileName('')
              setNewFileOpen(true)
            }}
          >
            <FilePlus2 />
            {t('files.newFile')}
          </Button>
          <Button
            variant="outline"
            size="sm"
            onClick={() => {
              setNewFolderName('')
              setNewFolderOpen(true)
            }}
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

      {/* File listing table */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('files.name')}</TableHead>
            <TableHead className="w-28">{t('files.size')}</TableHead>
            <TableHead className="w-44">{t('files.modified')}</TableHead>
            <TableHead className="w-28">{t('files.permissions')}</TableHead>
            <TableHead className="text-right w-36">{t('common.actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {sortedFiles.length === 0 && !loading && (
            <TableRow>
              <TableCell colSpan={5} className="text-center text-muted-foreground py-8">
                {t('files.empty')}
              </TableCell>
            </TableRow>
          )}
          {loading && files.length === 0 && (
            <TableRow>
              <TableCell colSpan={5} className="text-center py-8">
                <div className="flex items-center justify-center gap-2 text-muted-foreground">
                  <Loader2 className="h-4 w-4 animate-spin" />
                  {t('files.loading')}
                </div>
              </TableCell>
            </TableRow>
          )}
          {sortedFiles.map((entry) => (
            <ContextMenu key={entry.name}>
              <ContextMenuTrigger asChild>
                <TableRow
                  className="cursor-pointer hover:bg-secondary/50"
                  onClick={() =>
                    entry.isDir
                      ? handleDirectoryClick(entry)
                      : handleFileClick(entry)
                  }
                >
                  <TableCell>
                    <div className="flex items-center gap-2">
                      {entry.isDir ? (
                        <Folder className="h-4 w-4 text-blue-500 shrink-0" />
                      ) : entry.name.match(/\.(txt|md|log|conf|cfg|ini|json|xml|yaml|yml|toml|sh|bash|py|js|ts|jsx|tsx|html|css|scss|less|go|rs|rb|php|java|c|cpp|h|hpp|sql|lua|r|swift|kt|vue|svelte)$/i) ? (
                        <FileText className="h-4 w-4 text-amber-500 shrink-0" />
                      ) : (
                        <File className="h-4 w-4 text-muted-foreground shrink-0" />
                      )}
                      <span className="truncate">{entry.name}</span>
                    </div>
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
                      {!entry.isDir && (
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          title={t('files.edit')}
                          onClick={() => handleEditFile(entry)}
                        >
                          <Pencil />
                        </Button>
                      )}
                      {!entry.isDir && (
                        <Button
                          variant="ghost"
                          size="icon-xs"
                          title={t('files.download')}
                          onClick={() => handleDownload(entry)}
                        >
                          <Download />
                        </Button>
                      )}
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t('files.rename')}
                        onClick={() => {
                          setRenameTarget(entry)
                          setRenameNewName(entry.name)
                        }}
                      >
                        <Pencil className="h-3 w-3" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon-xs"
                        title={t('common.delete')}
                        onClick={() => setDeleteTarget(entry)}
                      >
                        <Trash2 />
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              </ContextMenuTrigger>
              <ContextMenuContent>
                <ContextMenuItem
                  onClick={() =>
                    entry.isDir
                      ? handleDirectoryClick(entry)
                      : handleFileClick(entry)
                  }
                >
                  {entry.isDir ? (
                    <Folder className="h-4 w-4" />
                  ) : (
                    <FileText className="h-4 w-4" />
                  )}
                  {entry.isDir
                    ? t('files.contextMenu.open')
                    : t('files.contextMenu.edit')}
                </ContextMenuItem>
                {!entry.isDir && (
                  <ContextMenuItem onClick={() => handleDownload(entry)}>
                    <Download className="h-4 w-4" />
                    {t('files.contextMenu.download')}
                  </ContextMenuItem>
                )}
                <ContextMenuSeparator />
                <ContextMenuItem
                  onClick={() => {
                    setRenameTarget(entry)
                    setRenameNewName(entry.name)
                  }}
                >
                  <Pencil className="h-4 w-4" />
                  {t('files.contextMenu.rename')}
                </ContextMenuItem>
                <ContextMenuItem
                  variant="destructive"
                  onClick={() => setDeleteTarget(entry)}
                >
                  <Trash2 className="h-4 w-4" />
                  {t('files.contextMenu.delete')}
                </ContextMenuItem>
              </ContextMenuContent>
            </ContextMenu>
          ))}
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
          <ContextMenuItem onClick={() => {
            setNewFileName('')
            setNewFileOpen(true)
          }}>
            <FilePlus2 className="h-4 w-4" />
            {t('files.newFile')}
          </ContextMenuItem>
          <ContextMenuItem onClick={() => {
            setNewFolderName('')
            setNewFolderOpen(true)
          }}>
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
      <Dialog open={editOpen} onOpenChange={(open) => !open && setEditOpen(false)}>
        <DialogContent className="sm:max-w-4xl">
          <DialogHeader>
            <DialogTitle>{t('files.editFile')}</DialogTitle>
            <DialogDescription>
              {editFilePath}
            </DialogDescription>
          </DialogHeader>
          {editLoading ? (
            <div className="flex items-center justify-center py-16">
              <Loader2 className="h-6 w-6 animate-spin text-muted-foreground" />
              <span className="ml-2 text-muted-foreground">{t('files.loadingFile')}</span>
            </div>
          ) : (
            <div className="space-y-4">
              <div className="rounded-md overflow-hidden border">
                <Editor
                  height="500px"
                  language={getLanguageFromFilename(editFileName)}
                  theme="vs-dark"
                  value={editContent}
                  onChange={(val) => setEditContent(val || '')}
                  options={{
                    minimap: { enabled: false },
                    fontSize: 14,
                    lineNumbers: 'on',
                    scrollBeyondLastLine: false,
                    wordWrap: 'on',
                    tabSize: 2,
                    insertSpaces: true,
                    automaticLayout: true,
                  }}
                />
              </div>
              <DialogFooter>
                <Button variant="outline" onClick={() => setEditOpen(false)}>
                  {t('common.cancel')}
                </Button>
                <Button onClick={handleSaveFile} disabled={editSaving}>
                  {editSaving ? (
                    <>
                      <Loader2 className="animate-spin" />
                      {t('common.saving')}
                    </>
                  ) : (
                    <>
                      <Save />
                      {t('common.save')}
                    </>
                  )}
                </Button>
              </DialogFooter>
            </div>
          )}
        </DialogContent>
      </Dialog>

      {/* New folder dialog */}
      <Dialog open={newFolderOpen} onOpenChange={setNewFolderOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('files.newFolderTitle')}</DialogTitle>
            <DialogDescription>
              {t('files.newFolderDescription', { path: currentPath })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="folder-name">{t('files.folderName')}</Label>
            <Input
              id="folder-name"
              placeholder={t('files.folderNamePlaceholder')}
              value={newFolderName}
              onChange={(e) => setNewFolderName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleCreateFolder()
              }}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setNewFolderOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleCreateFolder}
              disabled={newFolderCreating || !newFolderName.trim()}
            >
              {newFolderCreating ? (
                <>
                  <Loader2 className="animate-spin" />
                  {t('common.creating')}
                </>
              ) : (
                <>
                  <FolderPlus />
                  {t('files.createFolder')}
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* New file dialog */}
      <Dialog open={newFileOpen} onOpenChange={setNewFileOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('files.newFileTitle')}</DialogTitle>
            <DialogDescription>
              {t('files.newFileDescription', { path: currentPath })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="file-name">{t('files.fileName')}</Label>
            <Input
              id="file-name"
              placeholder={t('files.fileNamePlaceholder')}
              value={newFileName}
              onChange={(e) => setNewFileName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleCreateFile()
              }}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setNewFileOpen(false)}>
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleCreateFile}
              disabled={newFileCreating || !newFileName.trim()}
            >
              {newFileCreating ? (
                <>
                  <Loader2 className="animate-spin" />
                  {t('common.creating')}
                </>
              ) : (
                <>
                  <FilePlus2 />
                  {t('files.createFile')}
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Delete confirmation dialog */}
      <Dialog open={!!deleteTarget} onOpenChange={(open) => !open && setDeleteTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('files.deleteTitle')}</DialogTitle>
            <DialogDescription>
              {t('files.deleteConfirm', { name: deleteTarget?.name })}
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setDeleteTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              variant="destructive"
              onClick={handleDelete}
              disabled={deleteLoading}
            >
              {deleteLoading ? (
                <>
                  <Loader2 className="animate-spin" />
                  {t('files.deleting')}
                </>
              ) : (
                t('common.delete')
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* Rename dialog */}
      <Dialog open={!!renameTarget} onOpenChange={(open) => !open && setRenameTarget(null)}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{t('files.renameTitle')}</DialogTitle>
            <DialogDescription>
              {t('files.renameDescription', { name: renameTarget?.name })}
            </DialogDescription>
          </DialogHeader>
          <div className="space-y-2">
            <Label htmlFor="rename-input">{t('files.newName')}</Label>
            <Input
              id="rename-input"
              value={renameNewName}
              onChange={(e) => setRenameNewName(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter') handleRename()
              }}
            />
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRenameTarget(null)}>
              {t('common.cancel')}
            </Button>
            <Button
              onClick={handleRename}
              disabled={renameLoading || !renameNewName.trim() || renameNewName === renameTarget?.name}
            >
              {renameLoading ? (
                <>
                  <Loader2 className="animate-spin" />
                  {t('files.renaming')}
                </>
              ) : (
                t('files.renameAction')
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

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
                  backgroundColor: '#3182f6',
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
