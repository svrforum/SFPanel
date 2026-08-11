import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import ComposeEditor from '@/components/compose/ComposeEditor'
import { GitImportForm } from '@/components/compose/GitImportForm'

const DEFAULT_COMPOSE = `services:
  app:
    image: nginx:latest
    ports:
      - "8080:80"
`

/**
 * New-stack dialog: manual compose authoring + git import (GitImportForm),
 * symmetric with the git tab that was already extracted.
 */
export function CreateStackDialog({
  open,
  onOpenChange,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /** Fired with the new project name so the caller can refresh + navigate. */
  onCreated: (projectName: string) => void | Promise<void>
}) {
  const { t } = useTranslation()
  const [newName, setNewName] = useState('')
  const [newYaml, setNewYaml] = useState(DEFAULT_COMPOSE)
  const [creating, setCreating] = useState(false)

  const handleCreate = async () => {
    if (!newName.trim() || !newYaml.trim()) return
    setCreating(true)
    try {
      const created = newName.trim()
      await api.createComposeProject(created, newYaml)
      toast.success(t('docker.compose.createSuccess', { name: newName }))
      onOpenChange(false)
      setNewName('')
      setNewYaml(DEFAULT_COMPOSE)
      await onCreated(created)
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : t('docker.compose.createFailed'))
    } finally {
      setCreating(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="w-[calc(100vw-2rem)] md:w-full sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t('docker.compose.createTitle')}</DialogTitle>
          <DialogDescription>{t('docker.stacks.createDescription')}</DialogDescription>
        </DialogHeader>
        <Tabs defaultValue="manual" className="w-full">
          <TabsList>
            <TabsTrigger value="manual">{t('docker.stacks.manualTab', 'Manual')}</TabsTrigger>
            <TabsTrigger value="git">{t('docker.stacks.gitImportTab', 'Import from git')}</TabsTrigger>
          </TabsList>
          <TabsContent value="manual" className="space-y-4 pt-2">
            <div className="space-y-2">
              <Label htmlFor="project-name">{t('docker.compose.projectName')}</Label>
              <Input id="project-name" placeholder="e.g., my-project" value={newName}
                onChange={(e) => setNewName(e.target.value)} />
              <p className="text-[11px] text-muted-foreground">
                {t('docker.stacks.createPathHint', { path: `/opt/stacks/${newName || '{name}'}` })}
              </p>
            </div>
            <div className="space-y-2">
              <Label>{t('docker.compose.composeFile')}</Label>
              <div className="rounded-md overflow-hidden border">
                <ComposeEditor value={newYaml} onChange={setNewYaml} />
              </div>
            </div>
            <DialogFooter>
              <Button variant="outline" onClick={() => onOpenChange(false)}>{t('common.cancel')}</Button>
              <Button onClick={handleCreate} disabled={creating || !newName.trim() || !newYaml.trim()}>
                {creating ? t('common.creating') : t('common.create')}
              </Button>
            </DialogFooter>
          </TabsContent>
          <TabsContent value="git" className="pt-2">
            <GitImportForm
              onSuccess={(projectName) => {
                onOpenChange(false)
                void onCreated(projectName)
              }}
              onCancel={() => onOpenChange(false)}
            />
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  )
}
