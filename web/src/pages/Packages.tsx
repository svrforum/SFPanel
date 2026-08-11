import { useTranslation } from 'react-i18next'
import { OutputDialog, useSSEOutput } from '@/components/OutputDialog'
import { DockerStatusCard } from '@/pages/packages/components/DockerStatusCard'
import { DevToolsCard } from '@/pages/packages/components/DevToolsCard'
import { SystemUpdatesCard } from '@/pages/packages/components/SystemUpdatesCard'
import { PackageSearchCard } from '@/pages/packages/components/PackageSearchCard'

// Assembler for the Packages page. Each card owns its own state and API
// calls; the only shared piece is the streaming output dialog, which every
// long-running operation (installs, upgrades, removals) writes into.
export default function Packages() {
  const { t } = useTranslation()
  const output = useSSEOutput()

  return (
    <div className="space-y-6">
      {/* Page header */}
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('packages.title')}</h1>
        <p className="text-[13px] text-muted-foreground mt-1">{t('packages.subtitle')}</p>
      </div>

      <DockerStatusCard output={output} />
      <DevToolsCard output={output} />
      <SystemUpdatesCard output={output} />
      <PackageSearchCard output={output} />

      <OutputDialog state={output.state} onClose={output.closeOutput} />
    </div>
  )
}
