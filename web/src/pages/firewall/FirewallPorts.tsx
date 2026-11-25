import { useState, useEffect } from 'react'
import { useTranslation } from 'react-i18next'
import { Plus } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { PortMapTable } from '@/components/portmap/PortMapTable'
import { canAddRule } from '@/components/portmap/portRows'
import type { PortMapRow } from '@/types/api'
import { AddPortRuleDialog, type PortRuleTarget } from './AddPortRuleDialog'

export default function FirewallPorts() {
  const { t } = useTranslation()
  const [target, setTarget] = useState<PortRuleTarget | null>(null)
  // Bumped by an added rule, so the table reads the port map again.
  const [reloadKey, setReloadKey] = useState(0)
  // Whether ufw is what manages this host's firewall. Not on a Proxmox host,
  // which refuses ufw changes, nor where ufw is not installed. Older nodes do
  // not say, and always managed ufw.
  const [ufwManaged, setUfwManaged] = useState(true)

  useEffect(() => {
    api.getFirewallStatus()
      .then((s) => setUfwManaged((s.manager ?? 'ufw') === 'ufw'))
      .catch(() => {})
  }, [])

  // A rule for a row something listens on, in its table row and on its phone
  // card; revealed on hover where there is one.
  const addRule = (row: PortMapRow) => {
    if (!canAddRule(row)) return null
    const proto = row.proto === 'udp' ? 'udp' : 'tcp'
    return (
      <Button
        variant="ghost"
        size="sm"
        className="h-7 text-xs opacity-100 md:can-hover:opacity-0 md:group-hover:opacity-100 md:group-focus-within:opacity-100 transition-opacity"
        aria-label={t('firewall.ports.addRuleFor', { port: row.port, proto: proto.toUpperCase() })}
        onClick={() => setTarget({ port: row.port, proto })}
      >
        <Plus className="h-3.5 w-3.5 mr-1" />
        {t('firewall.ports.addRule')}
      </Button>
    )
  }

  return (
    <>
      <PortMapTable rowAction={ufwManaged ? addRule : undefined} reloadKey={reloadKey} />
      <AddPortRuleDialog target={target} onClose={() => setTarget(null)} onAdded={() => setReloadKey((k) => k + 1)} />
    </>
  )
}
