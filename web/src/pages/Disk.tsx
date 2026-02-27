import { useTranslation } from 'react-i18next'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import DiskOverview from '@/pages/disk/DiskOverview'
import DiskPartitions from '@/pages/disk/DiskPartitions'
import DiskFilesystems from '@/pages/disk/DiskFilesystems'
import DiskLVM from '@/pages/disk/DiskLVM'
import DiskRAID from '@/pages/disk/DiskRAID'
import DiskSwap from '@/pages/disk/DiskSwap'

export default function Disk() {
  const { t } = useTranslation()

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-[22px] font-bold tracking-tight">{t('disk.title')}</h1>
      </div>
      <Tabs defaultValue="overview">
        <TabsList className="bg-secondary/50 rounded-xl p-1">
          <TabsTrigger value="overview" className="rounded-lg text-[13px]">{t('disk.tabs.overview')}</TabsTrigger>
          <TabsTrigger value="partitions" className="rounded-lg text-[13px]">{t('disk.tabs.partitions')}</TabsTrigger>
          <TabsTrigger value="filesystems" className="rounded-lg text-[13px]">{t('disk.tabs.filesystems')}</TabsTrigger>
          <TabsTrigger value="lvm" className="rounded-lg text-[13px]">{t('disk.tabs.lvm')}</TabsTrigger>
          <TabsTrigger value="raid" className="rounded-lg text-[13px]">{t('disk.tabs.raid')}</TabsTrigger>
          <TabsTrigger value="swap" className="rounded-lg text-[13px]">{t('disk.tabs.swap')}</TabsTrigger>
        </TabsList>
        <TabsContent value="overview">
          <DiskOverview />
        </TabsContent>
        <TabsContent value="partitions">
          <DiskPartitions />
        </TabsContent>
        <TabsContent value="filesystems">
          <DiskFilesystems />
        </TabsContent>
        <TabsContent value="lvm">
          <DiskLVM />
        </TabsContent>
        <TabsContent value="raid">
          <DiskRAID />
        </TabsContent>
        <TabsContent value="swap">
          <DiskSwap />
        </TabsContent>
      </Tabs>
    </div>
  )
}
