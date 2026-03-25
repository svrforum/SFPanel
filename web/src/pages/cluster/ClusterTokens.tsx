import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { KeyRound, Copy, Check } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { toast } from 'sonner'

export default function ClusterTokens() {
  const { t } = useTranslation()
  const [ttl, setTtl] = useState('24h')
  const [generating, setGenerating] = useState(false)
  const [token, setToken] = useState('')
  const [expiresAt, setExpiresAt] = useState('')
  const [copied, setCopied] = useState(false)

  const handleGenerate = async () => {
    setGenerating(true)
    try {
      const result = await api.createClusterToken(ttl)
      setToken(result.token)
      setExpiresAt(result.expires_at)
    } catch (err) {
      toast.error(String(err))
    } finally {
      setGenerating(false)
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
                sudo sfpanel cluster join &lt;leader-ip&gt;:9443 {token}
              </div>
              <button
                onClick={() => handleCopy(`sudo sfpanel cluster join <leader-ip>:9443 ${token}`)}
                className="absolute right-3 top-3 p-1.5 rounded-lg hover:bg-accent transition-colors"
              >
                <Copy className="h-4 w-4 text-muted-foreground" />
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
