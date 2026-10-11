import { useTranslation } from 'react-i18next'
import { OutputDialog, useSSEOutput } from '@/components/OutputDialog'
import { DockerStatusCard } from '@/pages/packages/components/DockerStatusCard'
import { DevToolsCard } from '@/pages/packages/components/DevToolsCard'
import { SystemUpdatesCard } from '@/pages/packages/components/SystemUpdatesCard'
import { PackageSearchCard } from '@/pages/packages/components/PackageSearchCard'
import { TransactionBanner } from '@/pages/packages/components/TransactionBanner'
import { TransactionDialog } from '@/pages/packages/components/TransactionDialog'
import { useAptTransaction } from '@/pages/packages/useAptTransaction'

// Assembler for the Packages page. Each card owns its own state and API
// calls; what they share is the two output dialogs. Upgrades and installs
// from the list are package transactions — apt runs in a unit of its own on
// the node — and stream into theirs; the Docker and Node.js installers and
// removals write into the shared streaming output dialog.
export default function Packages() {
  const { t } = useTranslation()
  const output = useSSEOutput()
  const tx = useAptTransaction()

  return (
    <div className="space-y-6">
      {/* Page header */}
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('packages.title')}</h1>
        <p className="text-[13px] text-muted-foreground mt-1">{t('packages.subtitle')}</p>
      </div>

      <TransactionBanner tx={tx} />
      <DockerStatusCard output={output} />
      <DevToolsCard output={output} />
      <SystemUpdatesCard tx={tx} />
      <PackageSearchCard output={output} tx={tx} />

      <OutputDialog state={output.state} onClose={output.closeOutput} />
      <TransactionDialog state={tx.dialog} onClose={tx.close} onHide={tx.hide} />
    </div>
  )
}
