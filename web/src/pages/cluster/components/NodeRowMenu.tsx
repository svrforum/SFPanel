import { useId } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate } from 'react-router-dom'
import { ArrowDownToLine, ArrowRightLeft, ArrowUpToLine, ExternalLink, MoreHorizontal, Pencil, Tag, Trash2 } from 'lucide-react'
import type { ClusterNode } from '@/types/api'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { isAgent, isManager, nodeRoleKind } from '@/lib/clusterNodes'
import { openNode } from '@/lib/nodeSelection'

// One menu per node row, shared by the desktop table and the phone card, that
// offers only what a node of that kind can do. Labels, addresses, the
// leadership and removal are leader operations, and every node can ask for
// them: the server forwards a follower's request to the leader. Without a
// leader none of them can land, so only opening the node is left.
export function NodeRowMenu({ node, localId, leaderId, hasLeader, onLabels, onAddress, onTransfer, onRemove, onConvert, onPromote }: {
  node: ClusterNode
  localId: string
  leaderId: string
  hasLeader: boolean
  onLabels: (node: ClusterNode) => void
  onAddress: (node: ClusterNode) => void
  onTransfer: (node: ClusterNode) => void
  onRemove: (node: ClusterNode) => void
  onConvert?: (node: ClusterNode) => void
  onPromote?: (node: ClusterNode) => void
}) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const hintId = useId()
  // An agent's certificate names the address it registered with, so editing
  // it would only break the link; and the server has no agent to hand the
  // leadership to (404).
  const canAddress = hasLeader && isManager(node)
  // A voting manager, or an older record with no role: a non-voter cannot lead.
  const canTransfer = hasLeader && node.status === 'online' && node.id !== leaderId && nodeRoleKind(node, leaderId) === 'manager'
  // Removing this node is leaving, which is elsewhere; the leader hands the
  // leadership away first.
  const canRemove = hasLeader && node.id !== localId && node.id !== leaderId
  // The leader runs the conversion and refuses to convert itself: the item is
  // there but held back, with the way out next to it. A node that does not
  // answer cannot take part in either change.
  const canConvert = !!onConvert && hasLeader && isManager(node) && node.status === 'online'
  const convertHeld = node.id === leaderId
  const canPromote = !!onPromote && hasLeader && isAgent(node) && node.status === 'online'

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
        {canConvert && (
          <>
            <DropdownMenuItem
              disabled={convertHeld}
              aria-describedby={convertHeld ? hintId : undefined}
              onClick={() => onConvert?.(node)}
            >
              <ArrowDownToLine className="h-4 w-4 mr-2" />
              {t('cluster.convert.menu')}
            </DropdownMenuItem>
            {/* Outside the item: a disabled item takes no pointer, so a title would never show. */}
            {convertHeld && (
              <p id={hintId} className="px-2 pb-1.5 max-w-56 text-[11px] text-muted-foreground">
                {t('cluster.convert.leaderHint')}
              </p>
            )}
          </>
        )}
        {canPromote && (
          <DropdownMenuItem onClick={() => onPromote?.(node)}>
            <ArrowUpToLine className="h-4 w-4 mr-2" />
            {t('cluster.promote.menu')}
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
