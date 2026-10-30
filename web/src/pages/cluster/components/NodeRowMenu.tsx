import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { ArrowRightLeft, ExternalLink, MoreHorizontal, Pencil, Tag, Trash2 } from 'lucide-react'
import type { ClusterNode } from '@/types/api'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { isManager, nodeRoleKind } from '@/lib/clusterNodes'
import { openNode } from '@/lib/nodeSelection'

// One menu per node row, shared by the desktop table and the phone card, that
// offers only what a node of that kind can do. Labels, addresses, the
// leadership and removal are leader operations, and every node can ask for
// them: the server forwards a follower's request to the leader. Without a
// leader none of them can land, so only opening the node is left.
export function NodeRowMenu({ node, localId, leaderId, hasLeader, onLabels, onAddress, onTransfer, onRemove }: {
  node: ClusterNode
  localId: string
  leaderId: string
  hasLeader: boolean
  onLabels: (node: ClusterNode) => void
  onAddress: (node: ClusterNode) => void
  onTransfer: (node: ClusterNode) => void
  onRemove: (node: ClusterNode) => void
  /** Drawn by the conversion dialogs (Task 8); no item until then. */
  onConvert?: (node: ClusterNode) => void
  onPromote?: (node: ClusterNode) => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  // An agent's certificate names the address it registered with, so editing
  // it would only break the link; and the server has no agent to hand the
  // leadership to (404).
  const canAddress = hasLeader && isManager(node)
  // A voting manager, or an older record with no role: a non-voter cannot lead.
  const canTransfer = hasLeader && node.status === 'online' && node.id !== leaderId && nodeRoleKind(node, leaderId) === 'manager'
  // Removing this node is leaving, which is elsewhere; the leader hands the
  // leadership away first.
  const canRemove = hasLeader && node.id !== localId && node.id !== leaderId

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-xs" aria-label={t('cluster.nodes.actions')}>
          <MoreHorizontal className="h-4 w-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onClick={() => openNode(node.id, localId, navigate)}>
          <ExternalLink className="h-4 w-4 mr-2" />
          {t('cluster.nodes.open')}
        </DropdownMenuItem>
        {hasLeader && (
          <DropdownMenuItem onClick={() => onLabels(node)}>
            <Tag className="h-4 w-4 mr-2" />
            {t('cluster.nodes.editLabels')}
          </DropdownMenuItem>
        )}
        {canAddress && (
          <DropdownMenuItem onClick={() => onAddress(node)}>
            <Pencil className="h-4 w-4 mr-2" />
            {t('cluster.nodes.editAddress')}
          </DropdownMenuItem>
        )}
        {canTransfer && (
          <DropdownMenuItem onClick={() => onTransfer(node)}>
            <ArrowRightLeft className="h-4 w-4 mr-2" />
            {t('cluster.nodes.transferLeadership')}
          </DropdownMenuItem>
        )}
        {canRemove && (
          <>
            <DropdownMenuSeparator />
            <DropdownMenuItem variant="destructive" onClick={() => onRemove(node)}>
              <Trash2 className="h-4 w-4 mr-2" />
              {t('common.delete')}
            </DropdownMenuItem>
          </>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
