import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle } from 'lucide-react'
import { api } from '@/lib/api'
import { otherManagerAddress, roleChangeFailure, type RoleChangeFailure } from '@/lib/clusterNodes'
import { toastWarnings } from '@/lib/warnings'
import type { ClusterNode } from '@/types/api'
import { TypeToConfirmDialog } from '@/components/TypeToConfirmDialog'
import { RoleChangeFailureNote } from './RoleChangeProgress'

// Converting a manager to an agent wipes the cluster's secrets from that
// server and ends its panel's interface, so the operator types the node's
// name after reading what goes and what stays. A refusal stays in the dialog
// with the server's reason; nothing has changed then.
export function ConvertNodeDialog({ open, onOpenChange, node, localId, nodes, onStarted }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  node: ClusterNode | null
  localId: string
  nodes: ClusterNode[]
  /** 202: the conversion has started. */
  onStarted: (node: ClusterNode) => void
}) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<RoleChangeFailure | null>(null)

  // Each opening starts without the last one's refusal (TypeToConfirmDialog's pattern).
  const [prevOpen, setPrevOpen] = useState(open)
  if (prevOpen !== open) {
    setPrevOpen(open)
    setFailure(null)
  }

  const convert = async () => {
    if (!node) return
    setBusy(true)
    setFailure(null)
    try {
      const res = await api.convertClusterNode(node.id)
      toastWarnings(res.warnings, t)
      onStarted(node)
      onOpenChange(false)
    } catch (err) {
      setFailure(roleChangeFailure(err, 'CLUSTER_CONVERT_REFUSED'))
    } finally {
      setBusy(false)
    }
  }

  // An agent serves no interface: converting the manager this browser is on
  // takes this very page away, and the rest is followed from another manager.
  const isThisManager = !!node && node.id === localId

  return (
    <TypeToConfirmDialog
      open={open && !!node}
      onOpenChange={onOpenChange}
      title={t('cluster.convert.title', { name: node?.name ?? '' })}
      description={t('cluster.convert.intro')}
      confirmPhrase={node?.name ?? ''}
      confirmLabel={t('cluster.convert.menu')}
      prompt={t('cluster.convert.typeName')}
      loading={busy}
      onConfirm={convert}
    >
      <div className="space-y-3">
        <div className="space-y-1">
          <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('cluster.convert.erased')}</p>
          <p className="text-[13px]">{t('cluster.convert.erasedList')}</p>
        </div>
        <div className="space-y-1">
          <p className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('cluster.convert.kept')}</p>
          <p className="text-[13px]">{t('cluster.convert.keptList')}</p>
        </div>
        <p className="text-[13px] text-muted-foreground">{t('cluster.convert.noLogin')}</p>
        {isThisManager && (
          <div className="rounded-xl bg-warning/10 text-warning p-3 flex items-start gap-2">
            <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" aria-hidden />
            <p className="text-[13px]">{t('cluster.convert.thisManager', { address: otherManagerAddress(nodes, node.id) })}</p>
          </div>
        )}
        {failure && (
          <RoleChangeFailureNote failure={failure} refusedTitle={t('cluster.convert.refused')} noAnswer={t('cluster.convert.noAnswer')} />
        )}
      </div>
    </TypeToConfirmDialog>
  )
}
