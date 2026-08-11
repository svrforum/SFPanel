import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { DiffEditor } from '@monaco-editor/react'
import '@/lib/monaco' // configures the bundled (non-CDN) Monaco; lazy so it stays out of the entry bundle
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { Skeleton } from '@/components/ui/skeleton'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import type { DiffResult } from '@/types/api'
import { DiffSummaryHeader } from './DiffSummaryHeader'
import { DiffCategoryList } from './DiffCategoryList'

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  projectName: string
  proposedYaml: string
  onApply: () => void
}

export function DiffSheet({ open, onOpenChange, projectName, proposedYaml, onApply }: Props) {
  const { t } = useTranslation()
  const [data, setData] = useState<DiffResult | null>(null)
  const [deployedYaml, setDeployedYaml] = useState<string>('')
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)

  const loadDiff = useCallback(async () => {
    setLoading(true)
    setError(null)
    setData(null)
    setDeployedYaml('')
    try {
      const [diff, deployed] = await Promise.all([
        api.diffStack(projectName, proposedYaml),
        api.getComposeProject(projectName).then(d => d.yaml),
      ])
      setData(diff)
      setDeployedYaml(deployed)
    } catch (e) {
      const msg = e instanceof Error ? e.message : t('compose.diff.loadFailed', 'Failed to load the preview')
      setError(msg)
    } finally {
      setLoading(false)
    }
  }, [projectName, proposedYaml, t])

  useEffect(() => {
    if (open) loadDiff()
  }, [open, loadDiff])

  const isEmpty = !!data
    && data.summary.added === 0
    && data.summary.modified === 0
    && data.summary.removed === 0

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side="right"
        className="w-full sm:max-w-[640px] flex flex-col p-0"
      >
        <SheetHeader>
          <SheetTitle className="text-[14px]">{t('compose.diff.title', 'Preview changes')}</SheetTitle>
          <SheetDescription className="text-[12px]">
            {t('compose.diff.description', 'Compares the docker-compose.yml on disk with your proposed changes.')}
          </SheetDescription>
        </SheetHeader>

        <div className="flex-1 overflow-auto px-4 py-2 space-y-3">
          {loading && (
            <>
              <Skeleton className="h-12 w-full" />
              <Skeleton className="h-32 w-full" />
              <Skeleton className="h-32 w-full" />
            </>
          )}

          {error && !loading && (
            <div className="rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-[13px] text-destructive">
              <div className="font-medium mb-1">{t('compose.diff.loadFailed', 'Failed to load the preview')}</div>
              <div className="text-[12px] opacity-80">{error}</div>
            </div>
          )}

          {data && isEmpty && !loading && (
            <div className="text-center py-12 text-muted-foreground text-[13px]">
              🟢 {t('compose.diff.noChanges', 'No changes')}
            </div>
          )}

          {data && !isEmpty && !loading && (
            <>
              <DiffSummaryHeader summary={data.summary} />
              <Tabs defaultValue="categories">
                <TabsList>
                  <TabsTrigger value="categories">{t('compose.diff.categories', 'Categories')}</TabsTrigger>
                  <TabsTrigger value="raw">{t('compose.diff.raw', 'Raw text')}</TabsTrigger>
                </TabsList>
                <TabsContent value="categories" className="pt-2">
                  <DiffCategoryList byCategory={data.by_category} />
                </TabsContent>
                <TabsContent value="raw" className="pt-2">
                  <div className="border rounded-md overflow-hidden">
                    <DiffEditor
                      height="400px"
                      language="yaml"
                      theme="vs-dark"
                      original={deployedYaml}
                      modified={proposedYaml}
                      options={{
                        readOnly: true,
                        renderSideBySide: true,
                        minimap: { enabled: false },
                        fontSize: 12,
                      }}
                    />
                  </div>
                </TabsContent>
              </Tabs>
            </>
          )}
        </div>

        <SheetFooter className="border-t">
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t('common.close')}
          </Button>
          <Button
            onClick={onApply}
            disabled={!data || isEmpty || !!error || loading}
          >
            {t('compose.diff.apply', 'Apply as-is')}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
