import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import type { ClusterNode } from '@/types/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from '@/components/ui/dialog'
import { toast } from 'sonner'

// Edit a node's API / gRPC advertise addresses. Owns its field state and the
// PATCH call; the caller passes the node being edited and refreshes via onSaved.
export function EditNodeAddressDialog({
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
  const [apiAddr, setApiAddr] = useState('')
  const [grpcAddr, setGrpcAddr] = useState('')
  const [saving, setSaving] = useState(false)

  // Re-seed the fields from the node each time the dialog opens.
  useEffect(() => {
    if (!open || !node) return
    setApiAddr(node.api_address)
    setGrpcAddr(node.grpc_address)
  }, [open, node])

  const handleSave = async () => {
    if (!node) return
    setSaving(true)
    try {
      await api.updateClusterNodeAddress(node.id, apiAddr.trim(), grpcAddr.trim())
      toast.success(t('cluster.nodes.addressUpdated'))
      onOpenChange(false)
      onSaved()
    } catch (err) {
      toast.error(String(err))
    } finally {
      setSaving(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle className="text-[15px]">
            {t('cluster.nodes.editAddress')}{node ? ` — ${node.name}` : ''}
          </DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-1.5">
            <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
              {t('cluster.nodes.apiAddress')}
            </label>
            <Input
              value={apiAddr}
              onChange={(e) => setApiAddr(e.target.value)}
              className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
            />
          </div>
          <div className="space-y-1.5">
            <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
              {t('cluster.nodes.grpcAddress')}
            </label>
            <Input
              value={grpcAddr}
              onChange={(e) => setGrpcAddr(e.target.value)}
              className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
            />
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button className="rounded-xl" onClick={handleSave} disabled={saving}>
            {saving ? t('common.saving') : t('common.save')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
