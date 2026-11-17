import { useEffect, useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Server, Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { waitForServerBack } from '@/lib/restart'
import { toast } from 'sonner'

const message = (err: unknown) => (err instanceof Error ? err.message : String(err))

// What the cluster page shows while this node restarts into a new mode: after
// an init or a join here, after a leave or a disband on the overview.
export function RestartingCard({ description }: { description: string }) {
  const { t } = useTranslation()
  return (
    <div role="status" className="bg-card rounded-2xl p-8 card-shadow text-center space-y-4">
      <Loader2 className="h-10 w-10 text-primary mx-auto animate-spin" aria-hidden />
      <h2 className="text-[15px] font-semibold">{t('cluster.init.restarting')}</h2>
      <p className="text-[13px] text-muted-foreground">{description}</p>
    </div>
  )
}

// Onboarding screen shown while cluster mode is disabled: initialize a new
// cluster or join an existing one with a leader-issued token. Both paths
// activate the cluster inside the running panel; on success we still wait for
// the server to answer before reloading, so the page comes back in cluster
// mode either way.
export function ClusterInitForm() {
  const { t } = useTranslation()
  const [clusterName, setClusterName] = useState('sfpanel')
  const [interfaces, setInterfaces] = useState<{ name: string; address: string }[]>([])
  const [selectedAddr, setSelectedAddr] = useState('')
  // Whether the operator picked the address themselves. The first interface is
  // preselected for Init, but a join sends an address only when one was
  // picked: otherwise the server chooses the one that reaches the leader
  // (same subnet, Tailscale), which the first interface often is not.
  const addrPicked = useRef(false)
  const pickAddr = (addr: string) => {
    addrPicked.current = true
    setSelectedAddr(addr)
  }
  const [initializing, setInitializing] = useState(false)
  const [restarting, setRestarting] = useState(false)

  // Join form state
  const [leaderAddress, setLeaderAddress] = useState('')
  const [joinToken, setJoinToken] = useState('')
  const [caPin, setCaPin] = useState('')
  const [joining, setJoining] = useState(false)
  // The fields a submit found empty, each said under its field and cleared as
  // it is edited: a button held back until all were filled said nothing.
  const [missing, setMissing] = useState<{ name?: boolean; leader?: boolean; token?: boolean; ca?: boolean }>({})
  const fieldId = useId()
  const ids = { name: `${fieldId}-name`, addr: `${fieldId}-addr`, leader: `${fieldId}-leader`, token: `${fieldId}-token`, ca: `${fieldId}-ca` }

  useEffect(() => {
    api.getClusterInterfaces()
      .then((data) => {
        setInterfaces(data.interfaces || [])
        if (data.interfaces?.length > 0) {
          setSelectedAddr(data.interfaces[0].address)
        }
      })
      .catch(() => {})
  }, [])

  const handleInit = async () => {
    if (!clusterName.trim()) {
      setMissing((m) => ({ ...m, name: true }))
      return
    }
    setInitializing(true)
    try {
      await api.initCluster(clusterName.trim(), selectedAddr)
      toast.success(t('cluster.init.success'))
      setRestarting(true)
      // Wait for the self-restart (bounded, lib default 5 min), then reload;
      // on timeout reload anyway so the user isn't stuck on the spinner.
      waitForServerBack({ onTimeout: () => window.location.reload() })
    } catch (err) {
      toast.error(message(err))
      setInitializing(false)
    }
  }

  const handleJoin = async () => {
    const gaps = { leader: !leaderAddress.trim(), token: !joinToken.trim(), ca: !caPin.trim() }
    if (gaps.leader || gaps.token || gaps.ca) {
      setMissing((m) => ({ ...m, ...gaps }))
      return
    }
    setJoining(true)
    try {
      await api.joinCluster(leaderAddress.trim(), joinToken.trim(), caPin.trim(), (addrPicked.current && selectedAddr) || undefined)
      toast.success(t('cluster.join.success'))
      setRestarting(true)
      waitForServerBack({ onTimeout: () => window.location.reload() })
    } catch (err) {
      toast.error(message(err))
      setJoining(false)
    }
  }

  // A field's aria wiring and, when a submit found it empty, the line that says so.
  const invalid = (field: keyof typeof missing, hint?: string) => ({
    'aria-invalid': !!missing[field],
    'aria-describedby': [missing[field] ? `${ids[field]}-missing` : '', hint ?? ''].filter(Boolean).join(' ') || undefined,
  })
  const missingLine = (field: keyof typeof missing, key: string) =>
    missing[field] ? <p id={`${ids[field]}-missing`} className="text-[12px] text-destructive">{t(key)}</p> : null
  const noAutoCorrect = { autoCapitalize: 'off', autoCorrect: 'off', spellCheck: false } as const

  if (restarting) return <RestartingCard description={t('cluster.init.restartingDesc')} />

  return (
    <div className="bg-card rounded-2xl p-8 card-shadow space-y-6 max-w-lg mx-auto">
      <div className="text-center space-y-2">
        <Server className="h-12 w-12 text-muted-foreground mx-auto" />
        <h2 className="text-[15px] font-semibold">{t('cluster.notEnabled.title')}</h2>
        <p className="text-[13px] text-muted-foreground">
          {t('cluster.notEnabled.description')}
        </p>
      </div>

      <div className="space-y-4">
        {/* Cluster name */}
        <div className="space-y-1.5">
          <label htmlFor={ids.name} className="text-[12px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.init.clusterName')}
          </label>
          <Input
            id={ids.name}
            value={clusterName}
            onChange={(e) => { setClusterName(e.target.value); setMissing((m) => ({ ...m, name: false })) }}
            placeholder="sfpanel"
            {...invalid('name')}
            {...noAutoCorrect}
            className="h-9 rounded-xl bg-secondary/50 border-0 text-base md:text-[13px]"
          />
          {missingLine('name', 'cluster.init.nameRequired')}
        </div>

        {/* Advertise address */}
        <div className="space-y-1.5">
          <label id={ids.addr} htmlFor={interfaces.length > 0 ? undefined : `${ids.addr}-input`} className="text-[12px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.init.advertiseAddress')}
          </label>
          {/* One address is advertised to the other nodes: a radio each, so
              which one is chosen is not a 10% tint alone. */}
          {interfaces.length > 0 ? (
            <div role="radiogroup" aria-labelledby={ids.addr} aria-describedby={`${ids.addr}-desc`} className="space-y-1.5">
              {interfaces.map((iface) => (
                <label
                  key={`${iface.name}-${iface.address}`}
                  className={cn(
                    'w-full flex items-center gap-3 px-3 py-2 rounded-xl text-[13px] cursor-pointer transition-colors has-[:focus-visible]:ring-2 has-[:focus-visible]:ring-ring',
                    selectedAddr === iface.address
                      ? 'bg-primary/10 ring-1 ring-primary'
                      : 'bg-secondary/50 hover:bg-secondary'
                  )}
                >
                  <input
                    type="radio"
                    name={ids.addr}
                    value={iface.address}
                    checked={selectedAddr === iface.address}
                    onChange={() => pickAddr(iface.address)}
                    // A radio that is already checked fires no change, and the
                    // first address starts out checked: without this, pressing
                    // it to say "this one" sent no address at all.
                    onClick={() => pickAddr(iface.address)}
                    className="h-4 w-4 shrink-0 accent-primary"
                  />
                  <span className="font-medium">{iface.address}</span>
                  <span className="ml-auto text-[12px] text-muted-foreground">{iface.name}</span>
                </label>
              ))}
            </div>
          ) : (
            <Input
              id={`${ids.addr}-input`}
              value={selectedAddr}
              onChange={(e) => pickAddr(e.target.value)}
              placeholder="192.168.1.100"
              aria-describedby={`${ids.addr}-desc`}
              {...noAutoCorrect}
              className="h-9 rounded-xl bg-secondary/50 border-0 text-base md:text-[13px]"
            />
          )}
          <p id={`${ids.addr}-desc`} className="text-[12px] text-muted-foreground">
            {t('cluster.init.advertiseAddressDesc')}
          </p>
        </div>
      </div>

      <Button
        onClick={handleInit}
        disabled={initializing}
        className="w-full rounded-xl"
      >
        {initializing ? (
          <>
            <Loader2 className="h-4 w-4 mr-2 animate-spin" />
            {t('cluster.init.initializing')}
          </>
        ) : (
          t('cluster.init.button')
        )}
      </Button>

      {/* Divider */}
      <div className="flex items-center gap-3">
        <div className="flex-1 h-px bg-border" />
        <span className="text-[12px] text-muted-foreground">{t('cluster.join.or')}</span>
        <div className="flex-1 h-px bg-border" />
      </div>

      {/* Join existing cluster */}
      <div className="space-y-4">
        <div className="text-center space-y-1">
          <h3 className="text-[14px] font-semibold">{t('cluster.join.title')}</h3>
          <p className="text-[12px] text-muted-foreground">{t('cluster.join.description')}</p>
        </div>

        <div className="space-y-1.5">
          <label htmlFor={ids.leader} className="text-[12px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.join.leaderAddress')}
          </label>
          <Input
            id={ids.leader}
            value={leaderAddress}
            onChange={(e) => { setLeaderAddress(e.target.value); setMissing((m) => ({ ...m, leader: false })) }}
            placeholder={t('cluster.join.leaderAddressPlaceholder')}
            {...invalid('leader', `${ids.leader}-desc`)}
            {...noAutoCorrect}
            className="h-9 rounded-xl bg-secondary/50 border-0 text-base md:text-[13px]"
          />
          {missingLine('leader', 'cluster.join.leaderAddressRequired')}
          <p id={`${ids.leader}-desc`} className="text-[12px] text-muted-foreground">{t('cluster.join.leaderAddressDesc')}</p>
        </div>

        <div className="space-y-1.5">
          <label htmlFor={ids.token} className="text-[12px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.join.token')}
          </label>
          <Input
            id={ids.token}
            value={joinToken}
            onChange={(e) => { setJoinToken(e.target.value); setMissing((m) => ({ ...m, token: false })) }}
            placeholder={t('cluster.join.tokenPlaceholder')}
            {...invalid('token')}
            {...noAutoCorrect}
            className="h-9 rounded-xl bg-secondary/50 border-0 text-base md:text-[13px] font-mono"
          />
          {missingLine('token', 'cluster.join.tokenRequired')}
        </div>

        <div className="space-y-1.5">
          <label htmlFor={ids.ca} className="text-[12px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.join.caPin')}
          </label>
          <Input
            id={ids.ca}
            value={caPin}
            onChange={(e) => { setCaPin(e.target.value); setMissing((m) => ({ ...m, ca: false })) }}
            placeholder={t('cluster.join.caPinPlaceholder')}
            {...invalid('ca', `${ids.ca}-desc`)}
            {...noAutoCorrect}
            className="h-9 rounded-xl bg-secondary/50 border-0 text-base md:text-[13px] font-mono"
          />
          {missingLine('ca', 'cluster.join.caPinRequired')}
          <p id={`${ids.ca}-desc`} className="text-[12px] text-muted-foreground">{t('cluster.join.caPinDesc')}</p>
        </div>

        <Button
          onClick={handleJoin}
          disabled={joining}
          variant="outline"
          className="w-full rounded-xl"
        >
          {joining ? (
            <>
              <Loader2 className="h-4 w-4 mr-2 animate-spin" />
              {t('cluster.join.joining')}
            </>
          ) : (
            t('cluster.join.button')
          )}
        </Button>
      </div>
    </div>
  )
}
