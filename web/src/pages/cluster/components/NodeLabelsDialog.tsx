import { useEffect, useId, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Trash2 } from 'lucide-react'
import { api } from '@/lib/api'
import { GROUP_LABEL } from '@/lib/clusterNodes'
import type { ClusterNode } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { toast } from 'sonner'

// Edit a node's group and labels (add / remove key=value pairs, saved as a
// whole set). The group is the `group` label, edited in its own field and
// kept out of the list below it. Owns its working-copy state and the PATCH
// call; the caller passes the node being edited and refreshes via onSaved.
export function NodeLabelsDialog({
  open,
  onOpenChange,
  node,
  onSaved,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  node: ClusterNode | null
  onSaved: () => void
}) {
  const { t } = useTranslation()
  const [labels, setLabels] = useState<Record<string, string>>({})
  const [group, setGroup] = useState('')
  const groupId = useId()
  const [labelKey, setLabelKey] = useState('')
  const [labelValue, setLabelValue] = useState('')

  // Re-seed the working copy from the node each time the dialog opens.
  // setState inside an effect is intentional: the seed depends on which node
  // the dialog was opened for, an external input that changes between opens.
  useEffect(() => {
    if (!open || !node) return
    const rest = { ...(node.labels || {}) }
    delete rest[GROUP_LABEL]
    // eslint-disable-next-line react-hooks/set-state-in-effect
    setLabels(rest)
    setGroup(node.labels?.[GROUP_LABEL] ?? '')
    setLabelKey('')
    setLabelValue('')
  }, [open, node])

  const handleAddLabel = () => {
    if (!labelKey.trim()) return
    // Typed as a label, the group still belongs in its own field: kept in the
    // list it would be overwritten by that field on save.
    if (labelKey.trim() === GROUP_LABEL) setGroup(labelValue.trim())
    else setLabels(prev => ({ ...prev, [labelKey.trim()]: labelValue.trim() }))
    setLabelKey('')
    setLabelValue('')
  }

  const handleRemoveLabel = (key: string) => {
    setLabels(prev => {
      const next = { ...prev }
      delete next[key]
      return next
    })
  }

  const handleSave = async () => {
    if (!node) return
    // A blank group is no group: the key goes, rather than a group named "".
    const next = { ...labels }
    if (group.trim()) next[GROUP_LABEL] = group.trim()
    try {
      await api.updateClusterNodeLabels(node.id, next)
      toast.success(t('cluster.nodes.labelsUpdated'))
      onOpenChange(false)
      onSaved()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="text-[15px]">{t('cluster.nodes.editLabelsTitle', { name: node?.name ?? '' })}</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <label htmlFor={groupId} className="text-[11px] text-muted-foreground uppercase tracking-wider">
              {t('cluster.nodes.group')}
            </label>
            <Input
              id={groupId}
              value={group}
              onChange={(e) => setGroup(e.target.value)}
              aria-describedby={`${groupId}-hint`}
              className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
            />
            <p id={`${groupId}-hint`} className="text-[11px] text-muted-foreground">{t('cluster.nodes.groupHint')}</p>
          </div>

          {/* Existing labels */}
          <div className="space-y-2">
            {Object.entries(labels).map(([k, v]) => (
              <div key={k} className="flex items-center gap-2">
                <span className="inline-flex items-center px-2 py-1 rounded-lg text-[12px] font-medium bg-secondary flex-1">
                  {k} = {v}
                </span>
                <button
                  onClick={() => handleRemoveLabel(k)}
                  className="p-1 rounded hover:bg-destructive/10 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
                  aria-label={t('common.delete')}
                >
                  <Trash2 className="h-3.5 w-3.5 text-destructive" />
                </button>
              </div>
            ))}
            {Object.keys(labels).length === 0 && (
              <p className="text-[13px] text-muted-foreground text-center py-2">{t('cluster.nodes.noLabels')}</p>
            )}
          </div>

          {/* Add label form */}
          <div className="flex items-center gap-2">
            <Input
              value={labelKey}
              onChange={(e) => setLabelKey(e.target.value)}
              placeholder={t('cluster.nodes.labelKey')}
              className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] flex-1 min-w-0"
            />
            <Input
              value={labelValue}
              onChange={(e) => setLabelValue(e.target.value)}
              placeholder={t('cluster.nodes.labelValue')}
              className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] flex-1 min-w-0"
              onKeyDown={(e) => e.key === 'Enter' && handleAddLabel()}
            />
            <Button
              variant="outline"
              size="sm"
              className="rounded-xl"
              onClick={handleAddLabel}
              disabled={!labelKey.trim()}
              aria-label={t('cluster.nodes.addLabel')}
            >
              +
            </Button>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button className="rounded-xl" onClick={handleSave}>
            {t('common.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
