import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import type { ClusterTokenResponse } from '@/types/api'
import { CopyBlock } from '@/components/CopyBlock'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { toast } from 'sonner'
import { joinCommands } from '../joinCommands'

// A manager join token, as the token page made it before the add-agent dialog
// took that page's place: a lifetime to choose, the token, and the join
// command with the installer chained to it. onCreated lets the pending list
// show the new token.
export function ManagerTokenSection({ onCreated }: { onCreated: () => void }) {
  const { t } = useTranslation()
  const [ttl, setTtl] = useState('24h')
  const [generating, setGenerating] = useState(false)
  const [result, setResult] = useState<ClusterTokenResponse | null>(null)
  const [asRoot, setAsRoot] = useState(false)

  const handleGenerate = async () => {
    setGenerating(true)
    try {
      setResult(await api.createClusterToken(ttl))
      onCreated()
    } catch (err) {
      toast.error(err instanceof Error ? err.message : String(err))
    } finally {
      setGenerating(false)
    }
  }

  const commands = result
    ? joinCommands(result.advertise_address || '<leader-ip>', result.grpc_port || 3629, result.token, result.ca_pin ?? '', asRoot)
    : null

  return (
    <div className="space-y-3">
      <div className="flex items-end gap-3">
        <div className="space-y-1">
          <label htmlFor="manager-token-ttl" className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('cluster.tokens.ttl')}</label>
          <Input
            id="manager-token-ttl"
            value={ttl}
            onChange={(e) => setTtl(e.target.value)}
            className="w-32 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
            placeholder="24h"
          />
        </div>
        <Button onClick={handleGenerate} disabled={generating} variant="outline" className="rounded-xl">
          {generating ? t('common.creating') : t('cluster.tokens.generate')}
        </Button>
      </div>

      {result && commands && (
        <div className="space-y-3">
          <div className="flex items-center justify-between gap-2">
            <span className="text-[13px] font-medium">{t('cluster.tokens.generatedToken')}</span>
            <span className="text-[11px] text-muted-foreground">
              {t('cluster.tokens.expiresAt')}: {new Date(result.expires_at).toLocaleString()}
            </span>
          </div>
          <CopyBlock text={result.token} />

          {/* The server that joins may have no sudo: Proxmox VE's operator is root */}
          <label className="flex items-center gap-2 cursor-pointer">
            <Checkbox checked={asRoot} onCheckedChange={(v) => setAsRoot(v === true)} />
            <span className="text-[13px]">{t('cluster.tokens.asRoot')}</span>
          </label>

          <div>
            <span className="text-[11px] text-muted-foreground uppercase tracking-wider block mb-2">{t('cluster.tokens.joinCommand')}</span>
            <CopyBlock text={commands.join} />
          </div>

          {/* A server without SFPanel yet: the installer, then the same join */}
          <div>
            <span className="text-[11px] text-muted-foreground uppercase tracking-wider block mb-2">{t('cluster.tokens.installAndJoin')}</span>
            <CopyBlock text={commands.installAndJoin} />
          </div>
        </div>
      )}
    </div>
  )
}
