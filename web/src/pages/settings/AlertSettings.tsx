import { useState, useEffect, useCallback } from 'react'
import { api } from '@/lib/api'
import { toast } from 'sonner'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '@/components/ui/table'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Checkbox } from '@/components/ui/checkbox'
import { Trash2, Plus, Send, ChevronLeft, ChevronRight, X } from 'lucide-react'

// Types
interface AlertChannel {
  id: number
  name: string
  type: 'discord' | 'telegram'
  config: Record<string, string>
  enabled: boolean
  created_at: string
}

interface AlertRule {
  id: number
  name: string
  type: string
  threshold: number
  severity: 'info' | 'warning' | 'critical'
  cooldown: number
  channels: number[]
  node_scope: string
  nodes: string[]
  enabled: boolean
  created_at: string
}

interface AlertHistoryEntry {
  id: number
  type: string
  severity: string
  message: string
  node: string
  status: string
  created_at: string
}

const RULE_TYPES = [
  { value: 'cpu', label: 'CPU' },
  { value: 'memory', label: '메모리' },
  { value: 'disk', label: '디스크' },
  { value: 'container', label: '컨테이너' },
  { value: 'service', label: '서비스' },
  { value: 'login', label: '로그인' },
  { value: 'package', label: '패키지' },
]

const SEVERITY_OPTIONS = [
  { value: 'info', label: 'Info', color: 'bg-[#3182f6]/10 text-[#3182f6]' },
  { value: 'warning', label: 'Warning', color: 'bg-[#f59e0b]/10 text-[#f59e0b]' },
  { value: 'critical', label: 'Critical', color: 'bg-[#f04452]/10 text-[#f04452]' },
]

function getSeverityStyle(severity: string) {
  return SEVERITY_OPTIONS.find(s => s.value === severity)?.color || 'bg-secondary text-muted-foreground'
}

export default function AlertSettings() {
  // Channel state
  const [channels, setChannels] = useState<AlertChannel[]>([])
  const [showAddChannel, setShowAddChannel] = useState(false)
  const [channelForm, setChannelForm] = useState({
    name: '', type: 'discord' as 'discord' | 'telegram',
    webhook_url: '', bot_token: '', chat_id: '',
  })
  const [channelLoading, setChannelLoading] = useState(false)
  const [testingId, setTestingId] = useState<number | null>(null)

  // Rule state
  const [rules, setRules] = useState<AlertRule[]>([])
  const [showAddRule, setShowAddRule] = useState(false)
  const [ruleForm, setRuleForm] = useState({
    name: '', type: 'cpu', threshold: 90, severity: 'warning' as 'info' | 'warning' | 'critical',
    cooldown: 300, channels: [] as number[], node_scope: 'all', nodes: [] as string[],
  })
  const [ruleLoading, setRuleLoading] = useState(false)

  // History state
  const [history, setHistory] = useState<AlertHistoryEntry[]>([])
  const [historyTotal, setHistoryTotal] = useState(0)
  const [historyPage, setHistoryPage] = useState(1)
  const historyLimit = 20

  // Load data
  const loadChannels = useCallback(async () => {
    try {
      const data = await api.request<AlertChannel[]>('/alerts/channels')
      setChannels(data)
    } catch { /* ignore */ }
  }, [])

  const loadRules = useCallback(async () => {
    try {
      const data = await api.request<AlertRule[]>('/alerts/rules')
      setRules(data)
    } catch { /* ignore */ }
  }, [])

  const loadHistory = useCallback(async (page: number) => {
    try {
      const data = await api.request<{ items: AlertHistoryEntry[]; total: number }>(
        `/alerts/history?page=${page}&limit=${historyLimit}`
      )
      setHistory(data.items || [])
      setHistoryTotal(data.total || 0)
    } catch { /* ignore */ }
  }, [])

  useEffect(() => {
    loadChannels()
    loadRules()
    loadHistory(1)
  }, [loadChannels, loadRules, loadHistory])

  // Channel handlers
  async function handleAddChannel() {
    if (!channelForm.name.trim()) {
      toast.error('채널 이름을 입력하세요')
      return
    }
    const config: Record<string, string> = {}
    if (channelForm.type === 'discord') {
      if (!channelForm.webhook_url.trim()) { toast.error('Webhook URL을 입력하세요'); return }
      config.webhook_url = channelForm.webhook_url
    } else {
      if (!channelForm.bot_token.trim() || !channelForm.chat_id.trim()) {
        toast.error('Bot Token과 Chat ID를 입력하세요'); return
      }
      config.bot_token = channelForm.bot_token
      config.chat_id = channelForm.chat_id
    }
    setChannelLoading(true)
    try {
      await api.request('/alerts/channels', {
        method: 'POST',
        body: JSON.stringify({ name: channelForm.name, type: channelForm.type, config, enabled: true }),
      })
      toast.success('채널이 추가되었습니다')
      setShowAddChannel(false)
      setChannelForm({ name: '', type: 'discord', webhook_url: '', bot_token: '', chat_id: '' })
      loadChannels()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : '채널 추가 실패')
    } finally {
      setChannelLoading(false)
    }
  }

  async function handleToggleChannel(ch: AlertChannel) {
    try {
      await api.request(`/alerts/channels/${ch.id}`, {
        method: 'PUT',
        body: JSON.stringify({ ...ch, enabled: !ch.enabled }),
      })
      loadChannels()
    } catch { /* ignore */ }
  }

  async function handleDeleteChannel(id: number) {
    if (!window.confirm('이 채널을 삭제하시겠습니까?')) return
    try {
      await api.request(`/alerts/channels/${id}`, { method: 'DELETE' })
      toast.success('채널이 삭제되었습니다')
      loadChannels()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : '삭제 실패')
    }
  }

  async function handleTestChannel(id: number) {
    setTestingId(id)
    try {
      await api.request(`/alerts/channels/${id}/test`, { method: 'POST' })
      toast.success('테스트 알림이 전송되었습니다')
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : '테스트 실패')
    } finally {
      setTestingId(null)
    }
  }

  // Rule handlers
  async function handleAddRule() {
    if (!ruleForm.name.trim()) { toast.error('규칙 이름을 입력하세요'); return }
    if (ruleForm.channels.length === 0) { toast.error('알림 채널을 선택하세요'); return }
    setRuleLoading(true)
    try {
      await api.request('/alerts/rules', {
        method: 'POST',
        body: JSON.stringify({ ...ruleForm, enabled: true }),
      })
      toast.success('규칙이 추가되었습니다')
      setShowAddRule(false)
      setRuleForm({ name: '', type: 'cpu', threshold: 90, severity: 'warning', cooldown: 300, channels: [], node_scope: 'all', nodes: [] })
      loadRules()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : '규칙 추가 실패')
    } finally {
      setRuleLoading(false)
    }
  }

  async function handleToggleRule(rule: AlertRule) {
    try {
      await api.request(`/alerts/rules/${rule.id}`, {
        method: 'PUT',
        body: JSON.stringify({ ...rule, enabled: !rule.enabled }),
      })
      loadRules()
    } catch { /* ignore */ }
  }

  async function handleDeleteRule(id: number) {
    if (!window.confirm('이 규칙을 삭제하시겠습니까?')) return
    try {
      await api.request(`/alerts/rules/${id}`, { method: 'DELETE' })
      toast.success('규칙이 삭제되었습니다')
      loadRules()
    } catch (err: unknown) {
      toast.error(err instanceof Error ? err.message : '삭제 실패')
    }
  }

  // History handlers
  async function handleClearHistory() {
    if (!window.confirm('알림 히스토리를 모두 삭제하시겠습니까?')) return
    try {
      await api.request('/alerts/history', { method: 'DELETE' })
      toast.success('히스토리가 삭제되었습니다')
      setHistory([])
      setHistoryTotal(0)
      setHistoryPage(1)
    } catch { /* ignore */ }
  }

  function toggleRuleChannel(channelId: number) {
    setRuleForm(prev => ({
      ...prev,
      channels: prev.channels.includes(channelId)
        ? prev.channels.filter(id => id !== channelId)
        : [...prev.channels, channelId],
    }))
  }

  return (
    <div className="space-y-6">

      {/* ===== Section 1: 채널 관리 ===== */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-[15px] font-semibold">채널 관리</h3>
            <p className="text-[13px] text-muted-foreground mt-1">알림을 받을 Discord / Telegram 채널을 설정합니다</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="rounded-xl text-[13px]"
            onClick={() => setShowAddChannel(!showAddChannel)}
          >
            {showAddChannel ? <X className="h-3.5 w-3.5 mr-1.5" /> : <Plus className="h-3.5 w-3.5 mr-1.5" />}
            {showAddChannel ? '취소' : '채널 추가'}
          </Button>
        </div>

        {/* Add channel form */}
        {showAddChannel && (
          <div className="bg-secondary/30 rounded-xl p-4 mb-4 space-y-3">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label className="text-[13px]">채널 이름</Label>
                <Input
                  value={channelForm.name}
                  onChange={e => setChannelForm(f => ({ ...f, name: e.target.value }))}
                  placeholder="예: 운영팀 Discord"
                  className="rounded-xl"
                />
              </div>
              <div className="space-y-1.5">
                <Label className="text-[13px]">타입</Label>
                <Select value={channelForm.type} onValueChange={v => setChannelForm(f => ({ ...f, type: v as 'discord' | 'telegram' }))}>
                  <SelectTrigger className="rounded-xl w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="discord">Discord</SelectItem>
                    <SelectItem value="telegram">Telegram</SelectItem>
                  </SelectContent>
                </Select>
              </div>
            </div>
            {channelForm.type === 'discord' ? (
              <div className="space-y-1.5">
                <Label className="text-[13px]">Webhook URL</Label>
                <Input
                  value={channelForm.webhook_url}
                  onChange={e => setChannelForm(f => ({ ...f, webhook_url: e.target.value }))}
                  placeholder="https://discord.com/api/webhooks/..."
                  className="rounded-xl"
                />
              </div>
            ) : (
              <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
                <div className="space-y-1.5">
                  <Label className="text-[13px]">Bot Token</Label>
                  <Input
                    value={channelForm.bot_token}
                    onChange={e => setChannelForm(f => ({ ...f, bot_token: e.target.value }))}
                    placeholder="123456:ABC-DEF..."
                    className="rounded-xl"
                  />
                </div>
                <div className="space-y-1.5">
                  <Label className="text-[13px]">Chat ID</Label>
                  <Input
                    value={channelForm.chat_id}
                    onChange={e => setChannelForm(f => ({ ...f, chat_id: e.target.value }))}
                    placeholder="-1001234567890"
                    className="rounded-xl"
                  />
                </div>
              </div>
            )}
            <Button onClick={handleAddChannel} disabled={channelLoading} className="rounded-xl">
              {channelLoading ? '추가 중...' : '채널 추가'}
            </Button>
          </div>
        )}

        {/* Channel list */}
        {channels.length === 0 ? (
          <p className="text-[13px] text-muted-foreground py-4">설정된 채널이 없습니다</p>
        ) : (
          <div className="space-y-2">
            {channels.map(ch => (
              <div key={ch.id} className="flex items-center justify-between bg-secondary/30 rounded-xl px-4 py-3">
                <div className="flex items-center gap-3">
                  <span className={`inline-flex items-center px-2 py-0.5 rounded-full text-[10px] font-medium ${
                    ch.type === 'discord' ? 'bg-[#5865F2]/10 text-[#5865F2]' : 'bg-[#0088cc]/10 text-[#0088cc]'
                  }`}>
                    {ch.type === 'discord' ? 'Discord' : 'Telegram'}
                  </span>
                  <span className="text-[13px] font-medium">{ch.name}</span>
                  <span className={`inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium ${
                    ch.enabled ? 'bg-[#00c471]/10 text-[#00c471]' : 'bg-secondary text-muted-foreground'
                  }`}>
                    {ch.enabled ? '활성' : '비활성'}
                  </span>
                </div>
                <div className="flex items-center gap-1.5">
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2 text-[11px]"
                    onClick={() => handleToggleChannel(ch)}
                  >
                    {ch.enabled ? '비활성화' : '활성화'}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2 text-[11px]"
                    onClick={() => handleTestChannel(ch.id)}
                    disabled={testingId === ch.id}
                  >
                    <Send className="h-3 w-3 mr-1" />
                    {testingId === ch.id ? '전송 중...' : '테스트'}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2 text-[#f04452] hover:text-[#f04452]"
                    onClick={() => handleDeleteChannel(ch.id)}
                  >
                    <Trash2 className="h-3 w-3" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* ===== Section 2: 규칙 관리 ===== */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-[15px] font-semibold">규칙 관리</h3>
            <p className="text-[13px] text-muted-foreground mt-1">알림 트리거 조건을 설정합니다</p>
          </div>
          <Button
            variant="outline"
            size="sm"
            className="rounded-xl text-[13px]"
            onClick={() => setShowAddRule(!showAddRule)}
          >
            {showAddRule ? <X className="h-3.5 w-3.5 mr-1.5" /> : <Plus className="h-3.5 w-3.5 mr-1.5" />}
            {showAddRule ? '취소' : '규칙 추가'}
          </Button>
        </div>

        {/* Add rule form */}
        {showAddRule && (
          <div className="bg-secondary/30 rounded-xl p-4 mb-4 space-y-3">
            <div className="grid grid-cols-1 md:grid-cols-2 gap-3">
              <div className="space-y-1.5">
                <Label className="text-[13px]">규칙 이름</Label>
                <Input
                  value={ruleForm.name}
                  onChange={e => setRuleForm(f => ({ ...f, name: e.target.value }))}
                  placeholder="예: CPU 과부하 경고"
                  className="rounded-xl"
                />
              </div>
              <div className="space-y-1.5">
                <Label className="text-[13px]">타입</Label>
                <Select value={ruleForm.type} onValueChange={v => setRuleForm(f => ({ ...f, type: v }))}>
                  <SelectTrigger className="rounded-xl w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {RULE_TYPES.map(rt => (
                      <SelectItem key={rt.value} value={rt.value}>{rt.label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
            </div>
            <div className="grid grid-cols-1 md:grid-cols-3 gap-3">
              <div className="space-y-1.5">
                <Label className="text-[13px]">임계값 (%)</Label>
                <Input
                  type="number"
                  min={0}
                  max={100}
                  value={ruleForm.threshold}
                  onChange={e => setRuleForm(f => ({ ...f, threshold: Number(e.target.value) }))}
                  className="rounded-xl"
                />
              </div>
              <div className="space-y-1.5">
                <Label className="text-[13px]">심각도</Label>
                <Select value={ruleForm.severity} onValueChange={v => setRuleForm(f => ({ ...f, severity: v as 'info' | 'warning' | 'critical' }))}>
                  <SelectTrigger className="rounded-xl w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {SEVERITY_OPTIONS.map(s => (
                      <SelectItem key={s.value} value={s.value}>{s.label}</SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="space-y-1.5">
                <Label className="text-[13px]">쿨다운 (초)</Label>
                <Input
                  type="number"
                  min={0}
                  value={ruleForm.cooldown}
                  onChange={e => setRuleForm(f => ({ ...f, cooldown: Number(e.target.value) }))}
                  className="rounded-xl"
                />
              </div>
            </div>
            <div className="space-y-1.5">
              <Label className="text-[13px]">알림 채널</Label>
              {channels.length === 0 ? (
                <p className="text-[12px] text-muted-foreground">먼저 채널을 추가하세요</p>
              ) : (
                <div className="flex flex-wrap gap-2">
                  {channels.map(ch => (
                    <label key={ch.id} className="flex items-center gap-1.5 bg-secondary/50 rounded-lg px-2.5 py-1.5 cursor-pointer">
                      <Checkbox
                        checked={ruleForm.channels.includes(ch.id)}
                        onCheckedChange={() => toggleRuleChannel(ch.id)}
                      />
                      <span className="text-[12px]">{ch.name}</span>
                    </label>
                  ))}
                </div>
              )}
            </div>
            <div className="space-y-1.5">
              <Label className="text-[13px]">노드 범위</Label>
              <Select value={ruleForm.node_scope} onValueChange={v => setRuleForm(f => ({ ...f, node_scope: v }))}>
                <SelectTrigger className="rounded-xl w-full max-w-[200px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="all">전체 노드</SelectItem>
                  <SelectItem value="specific">특정 노드</SelectItem>
                </SelectContent>
              </Select>
            </div>
            <Button onClick={handleAddRule} disabled={ruleLoading} className="rounded-xl">
              {ruleLoading ? '추가 중...' : '규칙 추가'}
            </Button>
          </div>
        )}

        {/* Rule list */}
        {rules.length === 0 ? (
          <p className="text-[13px] text-muted-foreground py-4">설정된 규칙이 없습니다</p>
        ) : (
          <div className="space-y-2">
            {rules.map(rule => (
              <div key={rule.id} className="flex items-center justify-between bg-secondary/30 rounded-xl px-4 py-3">
                <div className="flex items-center gap-3 flex-wrap">
                  <span className="text-[13px] font-medium">{rule.name}</span>
                  <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-secondary text-muted-foreground">
                    {RULE_TYPES.find(rt => rt.value === rule.type)?.label || rule.type}
                  </span>
                  <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${getSeverityStyle(rule.severity)}`}>
                    {rule.severity}
                  </span>
                  <span className="text-[11px] text-muted-foreground">
                    임계값: {rule.threshold}% | 쿨다운: {rule.cooldown}초
                  </span>
                  <span className={`inline-flex items-center px-1.5 py-0.5 rounded-full text-[10px] font-medium ${
                    rule.enabled ? 'bg-[#00c471]/10 text-[#00c471]' : 'bg-secondary text-muted-foreground'
                  }`}>
                    {rule.enabled ? '활성' : '비활성'}
                  </span>
                </div>
                <div className="flex items-center gap-1.5 shrink-0">
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2 text-[11px]"
                    onClick={() => handleToggleRule(rule)}
                  >
                    {rule.enabled ? '비활성화' : '활성화'}
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2 text-[#f04452] hover:text-[#f04452]"
                    onClick={() => handleDeleteRule(rule.id)}
                  >
                    <Trash2 className="h-3 w-3" />
                  </Button>
                </div>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* ===== Section 3: 알림 히스토리 ===== */}
      <div className="bg-card rounded-2xl p-6 card-shadow">
        <div className="flex items-center justify-between mb-4">
          <div>
            <h3 className="text-[15px] font-semibold">알림 히스토리</h3>
            <p className="text-[13px] text-muted-foreground mt-1">발생한 알림 기록을 확인합니다</p>
          </div>
          {history.length > 0 && (
            <Button
              variant="outline"
              size="sm"
              className="rounded-xl text-[#f04452] hover:text-[#f04452]"
              onClick={handleClearHistory}
            >
              <Trash2 className="h-3.5 w-3.5 mr-1.5" />
              기록 삭제
            </Button>
          )}
        </div>

        {history.length === 0 ? (
          <p className="text-[13px] text-muted-foreground py-4">알림 기록이 없습니다</p>
        ) : (
          <>
            <div className="bg-card rounded-2xl card-shadow overflow-hidden">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead className="text-[11px]">시간</TableHead>
                    <TableHead className="text-[11px]">타입</TableHead>
                    <TableHead className="text-[11px]">심각도</TableHead>
                    <TableHead className="text-[11px]">메시지</TableHead>
                    <TableHead className="text-[11px]">노드</TableHead>
                    <TableHead className="text-[11px]">상태</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {history.map(entry => (
                    <TableRow key={entry.id}>
                      <TableCell className="text-[12px] text-muted-foreground whitespace-nowrap">
                        {new Date(entry.created_at).toLocaleString()}
                      </TableCell>
                      <TableCell>
                        <span className="inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium bg-secondary text-muted-foreground">
                          {RULE_TYPES.find(rt => rt.value === entry.type)?.label || entry.type}
                        </span>
                      </TableCell>
                      <TableCell>
                        <span className={`inline-flex items-center px-1.5 py-0.5 rounded text-[10px] font-medium ${getSeverityStyle(entry.severity)}`}>
                          {entry.severity}
                        </span>
                      </TableCell>
                      <TableCell className="text-[12px] max-w-[300px] truncate">{entry.message}</TableCell>
                      <TableCell className="text-[12px] text-muted-foreground">{entry.node || '-'}</TableCell>
                      <TableCell>
                        <span className={`text-[12px] ${
                          entry.status === 'resolved' ? 'text-[#00c471]' :
                          entry.status === 'fired' ? 'text-[#f04452]' :
                          'text-muted-foreground'
                        }`}>
                          {entry.status}
                        </span>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
            {historyTotal > historyLimit && (
              <div className="flex items-center justify-between mt-3">
                <span className="text-[12px] text-muted-foreground">
                  {historyPage} / {Math.ceil(historyTotal / historyLimit)} 페이지
                </span>
                <div className="flex gap-1.5">
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2"
                    disabled={historyPage <= 1}
                    onClick={() => { const p = historyPage - 1; setHistoryPage(p); loadHistory(p) }}
                  >
                    <ChevronLeft className="h-3.5 w-3.5" />
                  </Button>
                  <Button
                    variant="outline"
                    size="sm"
                    className="rounded-xl h-7 px-2"
                    disabled={historyPage >= Math.ceil(historyTotal / historyLimit)}
                    onClick={() => { const p = historyPage + 1; setHistoryPage(p); loadHistory(p) }}
                  >
                    <ChevronRight className="h-3.5 w-3.5" />
                  </Button>
                </div>
              </div>
            )}
          </>
        )}
      </div>
    </div>
  )
}
