import { useTranslation } from 'react-i18next'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import DockerContainers from '@/pages/docker/DockerContainers'
import DockerImages from '@/pages/docker/DockerImages'
import DockerVolumes from '@/pages/docker/DockerVolumes'
import DockerNetworks from '@/pages/docker/DockerNetworks'
import DockerCompose from '@/pages/docker/DockerCompose'

export default function Docker() {
  const { t } = useTranslation()

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('docker.title')}</h1>
      </div>
      <Tabs defaultValue="containers">
        <TabsList className="bg-secondary/50 rounded-xl p-1">
          <TabsTrigger value="containers" className="rounded-lg text-[13px]">{t('docker.tabs.containers')}</TabsTrigger>
          <TabsTrigger value="images" className="rounded-lg text-[13px]">{t('docker.tabs.images')}</TabsTrigger>
          <TabsTrigger value="volumes" className="rounded-lg text-[13px]">{t('docker.tabs.volumes')}</TabsTrigger>
          <TabsTrigger value="networks" className="rounded-lg text-[13px]">{t('docker.tabs.networks')}</TabsTrigger>
          <TabsTrigger value="compose" className="rounded-lg text-[13px]">{t('docker.tabs.compose')}</TabsTrigger>
        </TabsList>
        <TabsContent value="containers">
          <DockerContainers />
        </TabsContent>
        <TabsContent value="images">
          <DockerImages />
        </TabsContent>
        <TabsContent value="volumes">
          <DockerVolumes />
        </TabsContent>
        <TabsContent value="networks">
          <DockerNetworks />
        </TabsContent>
        <TabsContent value="compose">
          <DockerCompose />
        </TabsContent>
      </Tabs>
    </div>
  )
}
