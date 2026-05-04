import { useCallback, useEffect, useState } from 'react'
import { DiffEditor } from '@monaco-editor/react'
import {
  Sheet,
  SheetContent,
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
      const msg = e instanceof Error ? e.message : '미리보기를 불러올 수 없습니다.'
      setError(msg)
    } finally {
      setLoading(false)
    }
  }, [projectName, proposedYaml])

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
          <SheetTitle className="text-[14px]">변경사항 미리보기</SheetTitle>
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
              <div className="font-medium mb-1">미리보기를 불러올 수 없습니다</div>
              <div className="text-[12px] opacity-80">{error}</div>
            </div>
          )}

          {data && isEmpty && !loading && (
            <div className="text-center py-12 text-muted-foreground text-[13px]">
              🟢 변경 사항이 없습니다
            </div>
          )}

          {data && !isEmpty && !loading && (
            <>
              <DiffSummaryHeader summary={data.summary} />
              <Tabs defaultValue="categories">
                <TabsList>
                  <TabsTrigger value="categories">카테고리</TabsTrigger>
                  <TabsTrigger value="raw">원본 텍스트</TabsTrigger>
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
            닫기
          </Button>
          <Button
            onClick={onApply}
            disabled={!data || isEmpty || !!error || loading}
          >
            이대로 적용
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
