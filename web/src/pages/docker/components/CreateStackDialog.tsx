import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { isRiskyRefusal, riskLines } from '@/lib/composeRisk'
import { useConfirm } from '@/components/ConfirmDialog'
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
  stacksRoot,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /**
   * The folder stacks live in (server.stacks_path), read off an existing
   * stack's path. While there is none the dialog asks the node; a node too old
   * to say leaves the hint naming the setting instead of guessing /opt/stacks.
   */
  stacksRoot?: string
  /** Fired with the new project name so the caller can refresh + navigate. */
  onCreated: (projectName: string) => void | Promise<void>
}) {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [newName, setNewName] = useState('')
  const [newYaml, setNewYaml] = useState(DEFAULT_COMPOSE)
  const [creating, setCreating] = useState(false)

  // No stack to read the folder off: the node's overview carries it. Kept with
  // the node it came from, so another node's folder is never shown.
  const [asked, setAsked] = useState<{ node: string | null; path?: string }>()
  useEffect(() => {
    if (!open || stacksRoot) return
    let cancelled = false
    const node = api.currentNode
    api.getDashboardOverview('1h')
      .then((o) => { if (!cancelled) setAsked({ node, path: o.stacks_path }) })
      .catch(() => {})
    return () => { cancelled = true }
  }, [open, stacksRoot])
  const root = stacksRoot || (asked?.node === api.currentNode ? asked.path?.replace(/\/+$/, '') : undefined)

  const handleCreate = async () => {
    if (!newName.trim() || !newYaml.trim()) return
    setCreating(true)
    try {
      const created = newName.trim()
      try {
        await api.createComposeProject(created, newYaml)
      } catch (err: unknown) {
        // A risky refusal is the one the operator can lift: show what the
        // stack asks for once, and create it with the acknowledgement.
        if (!isRiskyRefusal(err)) throw err
        const ok = await confirm({
          title: t('docker.stacks.risky.title'),
          description: (
            <span>
              {t('docker.stacks.risky.body')}
              <span className="mt-2 block space-y-1 font-mono text-[12px] text-muted-foreground">
                {riskLines(err).map((line, index) => (
                  <span key={`${index}-${line}`} className="block">{line}</span>
                ))}
              </span>
            </span>
          ),
          confirmLabel: t('docker.stacks.risky.confirm'),
          danger: true,
        })
        if (!ok) return
        await api.createComposeProject(created, newYaml, true)
      }
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
                aria-describedby="project-name-hint"
                onChange={(e) => setNewName(e.target.value)} />
              <p id="project-name-hint" className="text-[12px] text-muted-foreground">
                {t('docker.stacks.nameRule')}{' '}
                {root
                  ? t('docker.stacks.createPathHint', { path: `${root}/${newName || '{name}'}` })
                  : t('docker.stacks.createPathHintUnknown', { name: newName || '{name}' })}
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
