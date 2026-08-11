import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Link2, Loader2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { getStateStyle } from './InterfaceCard'
import type { NetworkInterfaceInfo } from '@/types/api'

const BOND_MODES = [
  'balance-rr',
  'active-backup',
  'balance-xor',
  'broadcast',
  '802.3ad',
  'balance-tlb',
  'balance-alb',
]

/** Bond creation dialog — owns the form state; the page only opens it. */
export function BondCreateDialog({
  open,
  onOpenChange,
  availableSlaves,
  onCreated,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  availableSlaves: NetworkInterfaceInfo[]
  // Fired after a successful create so the page can mark pending changes + refetch.
  onCreated: () => void
}) {
  const { t } = useTranslation()
  const [bondName, setBondName] = useState('')
  const [bondMode, setBondMode] = useState('active-backup')
  const [bondSlaves, setBondSlaves] = useState<string[]>([])
  const [bondCreating, setBondCreating] = useState(false)

  const toggleBondSlave = (name: string) => {
    setBondSlaves((prev) =>
      prev.includes(name) ? prev.filter((s) => s !== name) : [...prev, name]
    )
  }

  const handleCreateBond = async () => {
    if (!bondName.trim() || bondSlaves.length === 0) return
    setBondCreating(true)
    try {
      await api.createBond({ name: bondName.trim(), mode: bondMode, slaves: bondSlaves })
      toast.success(t('network.bondCreated', { name: bondName }))
      onOpenChange(false)
      setBondName('')
      setBondMode('active-backup')
      setBondSlaves([])
      onCreated()
    } catch (err: unknown) {
      const message = err instanceof Error ? err.message : t('network.bondCreateFailed')
      toast.error(message)
    } finally {
      setBondCreating(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <Link2 className="h-5 w-5" />
            {t('network.createBond')}
          </DialogTitle>
          <DialogDescription>
            {t('network.createBondDesc')}
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="bond-name" className="text-[13px]">{t('network.bondName')}</Label>
            <Input
              id="bond-name"
              value={bondName}
              onChange={(e) => setBondName(e.target.value)}
              placeholder="bond0"
              className="pl-3 h-9 rounded-xl bg-secondary/50 border-0 text-[13px] font-mono"
            />
          </div>

          <div className="space-y-2">
            <Label htmlFor="bond-mode" className="text-[13px]">{t('network.bondMode')}</Label>
            <Select value={bondMode} onValueChange={setBondMode}>
              <SelectTrigger id="bond-mode" className="w-full rounded-xl">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {BOND_MODES.map((mode) => (
                  <SelectItem key={mode} value={mode}>
                    {mode}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>

          <div className="space-y-2">
            <Label className="text-[13px]">{t('network.bondSlaves')}</Label>
            {availableSlaves.length === 0 ? (
              <p className="text-[13px] text-muted-foreground">{t('network.noAvailableSlaves')}</p>
            ) : (
              <div className="space-y-1">
                {availableSlaves.map((iface) => (
                  <label
                    key={iface.name}
                    className={`flex items-center gap-3 px-3 py-2 rounded-xl cursor-pointer transition-all ${
                      bondSlaves.includes(iface.name)
                        ? 'bg-primary/10 text-primary'
                        : 'bg-secondary/50 text-foreground hover:bg-secondary'
                    }`}
                  >
                    <input
                      type="checkbox"
                      checked={bondSlaves.includes(iface.name)}
                      onChange={() => toggleBondSlave(iface.name)}
                      className="rounded"
                    />
                    <span className="text-[13px] font-medium">{iface.name}</span>
                    <span className={getStateStyle(iface.state)}>
                      {iface.state}
                    </span>
                  </label>
                ))}
              </div>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" className="rounded-xl" onClick={() => onOpenChange(false)}>
            {t('common.cancel')}
          </Button>
          <Button
            className="rounded-xl"
            onClick={handleCreateBond}
            disabled={bondCreating || !bondName.trim() || bondSlaves.length === 0}
          >
            {bondCreating && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t('common.create')}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
