import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { roleChangeFailure, type RoleChangeFailure } from '@/lib/clusterNodes'
import type { ClusterNode } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { RoleChangeFailureNote } from './RoleChangeProgress'

// Promoting an agent hands it the cluster's JWT key, CA key and admin
// account, and it only votes once the leader reaches its Raft port: the
// dialog says both before the operator agrees. Nothing is erased, so a plain
// confirmation is enough.
export function PromoteNodeDialog({ open, onOpenChange, node, onStarted }: {
  open: boolean
  onOpenChange: (open: boolean) => void
  node: ClusterNode | null
  /** 202: the promotion has started. */
  onStarted: (node: ClusterNode) => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog open={open && !!node} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle className="text-[15px]">{t('cluster.promote.title', { name: node?.name ?? '' })}</DialogTitle>
          <DialogDescription className="text-[13px]">{t('cluster.promote.body')}</DialogDescription>
        </DialogHeader>
        {/* Inside the content, so each opening starts without the last refusal. */}
        {node && <PromoteBody node={node} onStarted={onStarted} onClose={() => onOpenChange(false)} />}
      </DialogContent>
    </Dialog>
  )
}

function PromoteBody({ node, onStarted, onClose }: { node: ClusterNode; onStarted: (node: ClusterNode) => void; onClose: () => void }) {
  const { t } = useTranslation()
  const [busy, setBusy] = useState(false)
  const [failure, setFailure] = useState<RoleChangeFailure | null>(null)

  const promote = async () => {
    setBusy(true)
    setFailure(null)
    try {
      await api.promoteClusterNode(node.id)
      onStarted(node)
      onClose()
    } catch (err) {
      setFailure(roleChangeFailure(err, 'CLUSTER_PROMOTE_REFUSED'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <>
      {failure && (
        <RoleChangeFailureNote failure={failure} refusedTitle={t('cluster.promote.refused')} noAnswer={t('cluster.promote.noAnswer')} />
      )}
      <DialogFooter>
        <Button variant="outline" className="rounded-xl" onClick={onClose} disabled={busy}>{t('common.cancel')}</Button>
        <Button className="rounded-xl" loading={busy} onClick={promote}>{t('cluster.promote.menu')}</Button>
      </DialogFooter>
    </>
  )
}
