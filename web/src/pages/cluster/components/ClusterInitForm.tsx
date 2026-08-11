import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Server, Loader2 } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'
import { waitForServerBack } from '@/lib/restart'
import { toast } from 'sonner'

// Onboarding screen shown while cluster mode is disabled: initialize a new
// cluster or join an existing one with a leader-issued token. Both paths
// trigger a panel self-restart, so on success we wait for the server to come
// back and reload.
export function ClusterInitForm() {
  const { t } = useTranslation()
  const [clusterName, setClusterName] = useState('sfpanel')
  const [interfaces, setInterfaces] = useState<{ name: string; address: string }[]>([])
  const [selectedAddr, setSelectedAddr] = useState('')
  const [initializing, setInitializing] = useState(false)
  const [restarting, setRestarting] = useState(false)

  // Join form state
  const [leaderAddress, setLeaderAddress] = useState('')
  const [joinToken, setJoinToken] = useState('')
  const [joining, setJoining] = useState(false)

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
    if (!clusterName.trim()) return
    setInitializing(true)
    try {
      await api.initCluster(clusterName.trim(), selectedAddr)
      toast.success(t('cluster.init.success'))
      setRestarting(true)
      // Wait for the self-restart (bounded, lib default 5 min), then reload;
      // on timeout reload anyway so the user isn't stuck on the spinner.
      waitForServerBack({ onTimeout: () => window.location.reload() })
    } catch (err) {
      toast.error(String(err))
      setInitializing(false)
    }
  }

  const handleJoin = async () => {
    if (!leaderAddress.trim() || !joinToken.trim()) return
    setJoining(true)
    try {
      await api.joinCluster(leaderAddress.trim(), joinToken.trim(), selectedAddr || undefined)
      toast.success(t('cluster.join.success'))
      setRestarting(true)
      waitForServerBack({ onTimeout: () => window.location.reload() })
    } catch (err) {
      toast.error(String(err))
      setJoining(false)
    }
  }

  if (restarting) {
    return (
      <div className="bg-card rounded-2xl p-8 card-shadow text-center space-y-4">
        <Loader2 className="h-10 w-10 text-primary mx-auto animate-spin" />
        <h2 className="text-[15px] font-semibold">{t('cluster.init.restarting')}</h2>
        <p className="text-[13px] text-muted-foreground">{t('cluster.init.restartingDesc')}</p>
      </div>
    )
  }

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
          <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.init.clusterName')}
          </label>
          <Input
            value={clusterName}
            onChange={(e) => setClusterName(e.target.value)}
            placeholder="sfpanel"
            className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
          />
        </div>

        {/* Advertise address */}
        <div className="space-y-1.5">
          <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.init.advertiseAddress')}
          </label>
          {interfaces.length > 0 ? (
            <div className="space-y-1.5">
              {interfaces.map((iface) => (
                <button
                  key={`${iface.name}-${iface.address}`}
                  onClick={() => setSelectedAddr(iface.address)}
                  className={cn(
                    'w-full flex items-center justify-between px-3 py-2 rounded-xl text-[13px] transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0',
                    selectedAddr === iface.address
                      ? 'bg-primary/10 ring-1 ring-primary/20'
                      : 'bg-secondary/50 hover:bg-secondary'
                  )}
                >
                  <span className="font-medium">{iface.address}</span>
                  <span className="text-[11px] text-muted-foreground">{iface.name}</span>
                </button>
              ))}
            </div>
          ) : (
            <Input
              value={selectedAddr}
              onChange={(e) => setSelectedAddr(e.target.value)}
              placeholder="192.168.1.100"
              className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
            />
          )}
          <p className="text-[11px] text-muted-foreground">
            {t('cluster.init.advertiseAddressDesc')}
          </p>
        </div>
      </div>

      <Button
        onClick={handleInit}
        disabled={initializing || !clusterName.trim()}
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
        <span className="text-[11px] text-muted-foreground">{t('cluster.join.or')}</span>
        <div className="flex-1 h-px bg-border" />
      </div>

      {/* Join existing cluster */}
      <div className="space-y-4">
        <div className="text-center space-y-1">
          <h3 className="text-[14px] font-semibold">{t('cluster.join.title')}</h3>
          <p className="text-[12px] text-muted-foreground">{t('cluster.join.description')}</p>
        </div>

        <div className="space-y-1.5">
          <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.join.leaderAddress')}
          </label>
          <Input
            value={leaderAddress}
            onChange={(e) => setLeaderAddress(e.target.value)}
            placeholder={t('cluster.join.leaderAddressPlaceholder')}
            className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
          />
          <p className="text-[11px] text-muted-foreground">{t('cluster.join.leaderAddressDesc')}</p>
        </div>

        <div className="space-y-1.5">
          <label className="text-[11px] text-muted-foreground uppercase tracking-wider">
            {t('cluster.join.token')}
          </label>
          <Input
            value={joinToken}
            onChange={(e) => setJoinToken(e.target.value)}
            placeholder={t('cluster.join.tokenPlaceholder')}
            className="h-9 rounded-xl bg-secondary/50 border-0 text-[13px] font-mono"
          />
        </div>

        <Button
          onClick={handleJoin}
          disabled={joining || !leaderAddress.trim() || !joinToken.trim()}
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
