import { useTranslation } from 'react-i18next'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import FirewallRules from '@/pages/firewall/FirewallRules'
import FirewallPorts from '@/pages/firewall/FirewallPorts'
import FirewallFail2ban from '@/pages/firewall/FirewallFail2ban'

export default function Firewall() {
  const { t } = useTranslation()

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('firewall.title')}</h1>
      </div>
      <Tabs defaultValue="rules">
        <TabsList className="bg-secondary/50 rounded-xl p-1">
          <TabsTrigger value="rules" className="rounded-lg text-[13px]">{t('firewall.tabs.rules')}</TabsTrigger>
          <TabsTrigger value="ports" className="rounded-lg text-[13px]">{t('firewall.tabs.ports')}</TabsTrigger>
          <TabsTrigger value="fail2ban" className="rounded-lg text-[13px]">{t('firewall.tabs.fail2ban')}</TabsTrigger>
        </TabsList>
        <TabsContent value="rules">
          <FirewallRules />
        </TabsContent>
        <TabsContent value="ports">
          <FirewallPorts />
        </TabsContent>
        <TabsContent value="fail2ban">
          <FirewallFail2ban />
        </TabsContent>
      </Tabs>
    </div>
  )
}
