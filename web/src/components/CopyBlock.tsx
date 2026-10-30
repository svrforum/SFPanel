import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Copy, Check } from 'lucide-react'
import { copyText } from '@/lib/utils'
import { toast } from 'sonner'

// Monospace box with a top-right copy button. Each instance tracks its own
// copied state, so the check feedback shows on the button that was clicked
// (the old inline pair shared one flag and only the first button ever ticked).
export function CopyBlock({ text }: { text: string }) {
  const { t } = useTranslation()
  const [copied, setCopied] = useState(false)

  const handleCopy = async () => {
    if (await copyText(text)) {
      setCopied(true)
      toast.success(t('cluster.tokens.copied'))
      setTimeout(() => setCopied(false), 2000)
    } else {
      toast.error(t('cluster.tokens.copyFailed'))
    }
  }

  return (
    <div className="relative">
      <div className="bg-secondary/50 rounded-xl p-4 pr-12 font-mono text-[12px] break-all">
        {text}
      </div>
      <button
        onClick={handleCopy}
        title={t('common.copy')}
        aria-label={t('common.copy')}
        className="absolute right-3 top-3 p-1.5 rounded-lg hover:bg-accent transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
      >
        {copied ? <Check className="h-4 w-4 text-success" /> : <Copy className="h-4 w-4 text-muted-foreground" />}
      </button>
    </div>
  )
}
