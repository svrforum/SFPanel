import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { KeyRound, Copy, Check, Trash2 } from 'lucide-react'
import { api } from '@/lib/api'
import type { ClusterTokenInfo } from '@/types/api'
import { useConfirm } from '@/components/ConfirmDialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { toast } from 'sonner'

export default function ClusterTokens() {
  const { t } = useTranslation()
  const confirm = useConfirm()
  const [ttl, setTtl] = useState('24h')
  const [generating, setGenerating] = useState(false)
  const [token, setToken] = useState('')
  const [expiresAt, setExpiresAt] = useState('')
  const [grpcPort, setGrpcPort] = useState<number>(3629)
  const [advertise, setAdvertise] = useState<string>('<leader-ip>')
  const [copied, setCopied] = useState(false)
  const [activeTokens, setActiveTokens] = useState<ClusterTokenInfo[]>([])

  const loadTokens = () => {
    api.listClusterTokens()
      .then((res) => setActiveTokens(res.tokens || []))
      .catch((err) => toast.error(String(err)))
  }

  useEffect(() => {
    loadTokens()
  }, [])

  const handleGenerate = async () => {
    setGenerating(true)
    try {
      const result = await api.createClusterToken(ttl)
      setToken(result.token)
      setExpiresAt(result.expires_at)
      if (result.grpc_port) setGrpcPort(result.grpc_port)
      if (result.advertise_address) setAdvertise(result.advertise_address)
      loadTokens()
    } catch (err) {
      toast.error(String(err))
    } finally {
      setGenerating(false)
    }
  }

  const handleRevoke = async (id: string) => {
    if (!(await confirm({ title: t('cluster.tokens.revokeConfirm'), danger: true }))) return
    try {
      await api.revokeClusterToken(id)
      toast.success(t('cluster.tokens.revoked'))
      loadTokens()
    } catch (err) {
      toast.error(String(err))
    }
  }

  const handleCopy = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      toast.success(t('cluster.tokens.copied'))
      setTimeout(() => setCopied(false), 2000)
    } catch {
      toast.error('Failed to copy to clipboard')
    }
  }

  return (
    <div className="space-y-6">
      {/* Generate token */}
      <div className="bg-card rounded-2xl p-6 card-shadow space-y-4">
        <div className="flex items-center gap-3">
          <div className="h-8 w-8 rounded-lg bg-primary/10 flex items-center justify-center">
            <KeyRound className="h-4 w-4 text-primary" />
          </div>
          <div>
            <h2 className="text-[15px] font-semibold">{t('cluster.tokens.generateTitle')}</h2>
            <p className="text-[11px] text-muted-foreground">{t('cluster.tokens.generateDescription')}</p>
          </div>
        </div>

        <div className="flex items-center gap-3">
          <div className="space-y-1">
            <label className="text-[11px] text-muted-foreground uppercase tracking-wider">{t('cluster.tokens.ttl')}</label>
            <Input
              value={ttl}
              onChange={(e) => setTtl(e.target.value)}
              className="w-32 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
              placeholder="24h"
            />
          </div>
          <div className="pt-5">
            <Button
              onClick={handleGenerate}
              disabled={generating}
              className="rounded-xl"
            >
              {generating ? t('common.creating') : t('cluster.tokens.generate')}
            </Button>
          </div>
        </div>
      </div>

      {/* Token result */}
      {token && (
        <div className="bg-card rounded-2xl p-6 card-shadow space-y-4">
          <div className="flex items-center justify-between">
            <h3 className="text-[15px] font-semibold">{t('cluster.tokens.generatedToken')}</h3>
            <span className="text-[11px] text-muted-foreground">
              {t('cluster.tokens.expiresAt')}: {new Date(expiresAt).toLocaleString()}
            </span>
          </div>

          {/* Token value */}
          <div className="relative">
            <div className="bg-secondary/50 rounded-xl p-4 pr-12 font-mono text-[12px] break-all">
              {token}
            </div>
            <button
              onClick={() => handleCopy(token)}
              className="absolute right-3 top-3 p-1.5 rounded-lg hover:bg-accent transition-colors"
            >
              {copied ? <Check className="h-4 w-4 text-[#00c471]" /> : <Copy className="h-4 w-4 text-muted-foreground" />}
            </button>
          </div>

          {/* Join command */}
          <div>
            <label className="text-[11px] text-muted-foreground uppercase tracking-wider block mb-2">
              {t('cluster.tokens.joinCommand')}
            </label>
            <div className="relative">
              <div className="bg-secondary/50 rounded-xl p-4 pr-12 font-mono text-[12px]">
                sudo sfpanel cluster join {advertise}:{grpcPort} {token}
              </div>
              <button
                onClick={() => handleCopy(`sudo sfpanel cluster join ${advertise}:${grpcPort} ${token}`)}
                className="absolute right-3 top-3 p-1.5 rounded-lg hover:bg-accent transition-colors"
              >
                <Copy className="h-4 w-4 text-muted-foreground" />
              </button>
            </div>
          </div>
        </div>
      )}

      {/* Active tokens */}
      <div className="bg-card rounded-2xl p-6 card-shadow space-y-4">
        <div className="flex items-center gap-3">
          <div className="h-8 w-8 rounded-lg bg-primary/10 flex items-center justify-center">
            <KeyRound className="h-4 w-4 text-primary" />
          </div>
          <div>
            <h2 className="text-[15px] font-semibold">{t('cluster.tokens.activeTitle')}</h2>
            <p className="text-[11px] text-muted-foreground">{t('cluster.tokens.activeDescription')}</p>
          </div>
        </div>

        {activeTokens.length === 0 ? (
          <p className="text-[13px] text-muted-foreground">{t('cluster.tokens.empty')}</p>
        ) : (
          <div className="divide-y divide-border">
            {activeTokens.map((tok) => (
              <div key={tok.id} className="flex items-center justify-between py-3">
                <div className="min-w-0">
                  <div className="font-mono text-[13px] break-all">{tok.masked}</div>
                  <div className="text-[11px] text-muted-foreground">
                    {t('cluster.tokens.createdBy')}: {tok.created_by} · {t('cluster.tokens.expiresAt')}: {new Date(tok.expires_at).toLocaleString()}
                  </div>
                </div>
                <Button
                  variant="ghost"
                  size="sm"
                  className="h-7 w-7 p-0 shrink-0 text-[#f04452] hover:text-[#f04452] hover:bg-[#f04452]/10"
                  onClick={() => handleRevoke(tok.id)}
                  title={t('cluster.tokens.revoke')}
                >
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
          </div>
        )}
      </div>
    </div>
  )
}
