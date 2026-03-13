import { useState, useEffect, useRef } from 'react'
import { Monitor, ChevronDown } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import type { ClusterStatus, ClusterNode } from '@/types/api'
import { cn } from '@/lib/utils'

interface NodeSelectorProps {
  collapsed?: boolean
}

export default function NodeSelector({ collapsed }: NodeSelectorProps) {
  const { t } = useTranslation()
  const [clusterStatus, setClusterStatus] = useState<ClusterStatus | null>(null)
  const [nodes, setNodes] = useState<ClusterNode[]>([])
  const [localId, setLocalId] = useState('')
  const [selectedNode, setSelectedNode] = useState<string | null>(null)
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)

  useEffect(() => {
    api.getClusterStatus()
      .then((status) => {
        setClusterStatus(status)
        if (status.enabled && status.local_id) {
          setLocalId(status.local_id)
          api.getClusterNodes().then((data) => {
            setNodes(data.nodes)
          }).catch(() => {})
        }
      })
      .catch(() => {})
  }, [])

  useEffect(() => {
    api.setCurrentNode(selectedNode)
  }, [selectedNode])

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (ref.current && !ref.current.contains(e.target as HTMLElement)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  if (!clusterStatus?.enabled) return null

  const currentNode = selectedNode
    ? nodes.find((n) => n.id === selectedNode)
    : nodes.find((n) => n.id === localId)

  const statusColor = (status: string) => {
    switch (status) {
      case 'online': return 'bg-[#00c471]'
      case 'suspect': return 'bg-[#f59e0b]'
      case 'offline': return 'bg-[#f04452]'
      default: return 'bg-muted-foreground'
    }
  }

  if (collapsed) {
    return (
      <div ref={ref} className="relative px-2 py-1">
        <button
          onClick={() => setOpen(!open)}
          className="flex flex-col items-center gap-1 w-full py-1.5 rounded-xl hover:bg-accent transition-colors"
          title={currentNode?.name || t('layout.cluster.selectNode')}
        >
          <Monitor className="h-[18px] w-[18px] text-muted-foreground" />
          <span className={cn('h-1.5 w-1.5 rounded-full', statusColor(currentNode?.status || ''))} />
        </button>
        {open && (
          <div className="absolute left-full top-0 ml-2 z-50 w-52 bg-popover border border-border rounded-xl card-shadow py-1">
            {nodes.map((node) => (
              <button
                key={node.id}
                onClick={() => {
                  setSelectedNode(node.id === localId ? null : node.id)
                  setOpen(false)
                }}
                className={cn(
                  'flex items-center gap-2 w-full px-3 py-2 text-[13px] hover:bg-accent transition-colors',
                  (selectedNode === node.id || (!selectedNode && node.id === localId)) && 'bg-primary/10 text-primary'
                )}
              >
                <span className={cn('h-2 w-2 rounded-full shrink-0', statusColor(node.status))} />
                <span className="truncate">{node.name}</span>
                {node.id === localId && (
                  <span className="text-[10px] text-muted-foreground ml-auto">({t('layout.cluster.localNode')})</span>
                )}
              </button>
            ))}
          </div>
        )}
      </div>
    )
  }

  return (
    <div ref={ref} className="relative px-3 py-1">
      <button
        onClick={() => setOpen(!open)}
        className="flex items-center gap-2 w-full px-3 py-2 rounded-xl hover:bg-accent transition-colors text-[13px]"
      >
        <Monitor className="h-[16px] w-[16px] text-muted-foreground shrink-0" />
        <span className={cn('h-2 w-2 rounded-full shrink-0', statusColor(currentNode?.status || ''))} />
        <span className="truncate font-medium text-foreground/80">
          {currentNode?.name || t('layout.cluster.selectNode')}
        </span>
        {!selectedNode && <span className="text-[10px] text-muted-foreground">({t('layout.cluster.localNode')})</span>}
        <ChevronDown className={cn('h-3.5 w-3.5 text-muted-foreground ml-auto shrink-0 transition-transform', open && 'rotate-180')} />
      </button>
      {open && (
        <div className="absolute left-3 right-3 top-full mt-1 z-50 bg-popover border border-border rounded-xl card-shadow py-1">
          {nodes.map((node) => (
            <button
              key={node.id}
              onClick={() => {
                setSelectedNode(node.id === localId ? null : node.id)
                setOpen(false)
              }}
              className={cn(
                'flex items-center gap-2 w-full px-3 py-2 text-[13px] hover:bg-accent transition-colors',
                (selectedNode === node.id || (!selectedNode && node.id === localId)) && 'bg-primary/10 text-primary'
              )}
            >
              <span className={cn('h-2 w-2 rounded-full shrink-0', statusColor(node.status))} />
              <span className="truncate">{node.name}</span>
              {node.id === localId && (
                <span className="text-[10px] text-muted-foreground">({t('layout.cluster.localNode')})</span>
              )}
              {node.id === clusterStatus?.leader_id && (
                <span className="inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-[#3182f6]/10 text-[#3182f6] ml-auto">
                  {t('layout.cluster.leader')}
                </span>
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
