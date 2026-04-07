import { lazy, Suspense, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { BrowserRouter, Routes, Route, Navigate, useNavigate, useLocation } from 'react-router-dom'
import { Toaster } from '@/components/ui/sonner'
import { api } from '@/lib/api'
import Layout from '@/components/Layout'
import { ErrorBoundary } from './components/ErrorBoundary'

// Lazy-loaded pages for code splitting
const Login = lazy(() => import('@/pages/Login'))
const Setup = lazy(() => import('@/pages/Setup'))
const Dashboard = lazy(() => import('@/pages/Dashboard'))
const Docker = lazy(() => import('@/pages/Docker'))
const DockerStacks = lazy(() => import('@/pages/docker/DockerStacks'))
const DockerContainers = lazy(() => import('@/pages/docker/DockerContainers'))
const DockerImages = lazy(() => import('@/pages/docker/DockerImages'))
const DockerVolumes = lazy(() => import('@/pages/docker/DockerVolumes'))
const DockerNetworks = lazy(() => import('@/pages/docker/DockerNetworks'))
const Files = lazy(() => import('@/pages/Files'))
const CronJobs = lazy(() => import('@/pages/CronJobs'))
const Logs = lazy(() => import('@/pages/Logs'))
const Processes = lazy(() => import('@/pages/Processes'))
const Services = lazy(() => import('@/pages/Services'))
const Network = lazy(() => import('@/pages/Network'))
const NetworkInterfaces = lazy(() => import('@/pages/network/NetworkInterfaces'))
const NetworkWireGuard = lazy(() => import('@/pages/network/NetworkWireGuard'))
const NetworkTailscale = lazy(() => import('@/pages/network/NetworkTailscale'))
const Disk = lazy(() => import('@/pages/Disk'))
const DiskOverview = lazy(() => import('@/pages/disk/DiskOverview'))
const DiskPartitions = lazy(() => import('@/pages/disk/DiskPartitions'))
const DiskFilesystems = lazy(() => import('@/pages/disk/DiskFilesystems'))
const DiskLVM = lazy(() => import('@/pages/disk/DiskLVM'))
const DiskRAID = lazy(() => import('@/pages/disk/DiskRAID'))
const DiskSwap = lazy(() => import('@/pages/disk/DiskSwap'))
const Firewall = lazy(() => import('@/pages/Firewall'))
const FirewallRules = lazy(() => import('@/pages/firewall/FirewallRules'))
const FirewallPorts = lazy(() => import('@/pages/firewall/FirewallPorts'))
const FirewallFail2ban = lazy(() => import('@/pages/firewall/FirewallFail2ban'))
const FirewallDocker = lazy(() => import('@/pages/firewall/FirewallDocker'))
const FirewallLogs = lazy(() => import('@/pages/firewall/FirewallLogs'))
const Cluster = lazy(() => import('@/pages/Cluster'))
const ClusterOverview = lazy(() => import('@/pages/cluster/ClusterOverview'))
const ClusterNodes = lazy(() => import('@/pages/cluster/ClusterNodes'))
const ClusterTokens = lazy(() => import('@/pages/cluster/ClusterTokens'))
const AppStore = lazy(() => import('@/pages/AppStore'))
const Packages = lazy(() => import('@/pages/Packages'))
const Settings = lazy(() => import('@/pages/Settings'))
const Terminal = lazy(() => import('@/pages/Terminal'))
const Connect = lazy(() => import('@/pages/Connect'))

function PageLoader() {
  return (
    <div className="flex items-center justify-center h-32">
      <div className="h-5 w-5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
    </div>
  )
}

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  if (!api.isAuthenticated()) {
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}

let setupChecked = false

function SetupGuard({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const location = useLocation()
  const [checking, setChecking] = useState(!setupChecked)

  useEffect(() => {
    if (setupChecked || location.pathname === '/setup' || location.pathname === '/connect') {
      setChecking(false)
      return
    }

    api.getSetupStatus()
      .then((data) => {
        if (data.setup_required) {
          navigate('/setup', { replace: true })
        } else {
          setupChecked = true
        }
      })
      .catch(() => {
        setupChecked = true
      })
      .finally(() => {
        setChecking(false)
      })
  }, [navigate, location.pathname])

  if (checking) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-slate-50">
        <p className="text-muted-foreground">{t('common.loading')}</p>
      </div>
    )
  }

  return <>{children}</>
}

function TauriGuard({ children }: { children: React.ReactNode }) {
  const location = useLocation()
  if (api.isTauri && !api.isConnected() && location.pathname !== '/connect') {
    return <Navigate to="/connect" replace />
  }
  return <>{children}</>
}

export default function App() {
  return (
    <ErrorBoundary>
    <BrowserRouter>
      <TauriGuard>
        <SetupGuard>
          <Suspense fallback={<PageLoader />}>
            <Routes>
              <Route path="/connect" element={<Connect />} />
              <Route path="/login" element={<Login />} />
              <Route path="/setup" element={<Setup />} />
              <Route path="/" element={
                <ProtectedRoute>
                  <Layout />
                </ProtectedRoute>
              }>
                <Route index element={<Navigate to="/dashboard" replace />} />
                <Route path="dashboard" element={<Dashboard />} />
                <Route path="docker" element={<Docker />}>
                  <Route index element={<Navigate to="stacks" replace />} />
                  <Route path="stacks" element={<DockerStacks />} />
                  <Route path="stacks/:name" element={<DockerStacks />} />
                  <Route path="containers" element={<DockerContainers />} />
                  <Route path="images" element={<DockerImages />} />
                  <Route path="volumes" element={<DockerVolumes />} />
                  <Route path="networks" element={<DockerNetworks />} />
                </Route>
                <Route path="cluster" element={<Cluster />}>
                  <Route index element={<Navigate to="overview" replace />} />
                  <Route path="overview" element={<ClusterOverview />} />
                  <Route path="nodes" element={<ClusterNodes />} />
                  <Route path="tokens" element={<ClusterTokens />} />
                </Route>
                <Route path="appstore" element={<AppStore />} />
                <Route path="files" element={<Files />} />
                <Route path="cron" element={<CronJobs />} />
                <Route path="logs" element={<Logs />} />
                <Route path="processes" element={<Processes />} />
                <Route path="services" element={<Services />} />
                <Route path="network" element={<Network />}>
                  <Route index element={<Navigate to="interfaces" replace />} />
                  <Route path="interfaces" element={<NetworkInterfaces />} />
                  <Route path="wireguard" element={<NetworkWireGuard />} />
                  <Route path="tailscale" element={<NetworkTailscale />} />
                </Route>
                <Route path="disk" element={<Disk />}>
                  <Route index element={<Navigate to="overview" replace />} />
                  <Route path="overview" element={<DiskOverview />} />
                  <Route path="partitions" element={<DiskPartitions />} />
                  <Route path="filesystems" element={<DiskFilesystems />} />
                  <Route path="lvm" element={<DiskLVM />} />
                  <Route path="raid" element={<DiskRAID />} />
                  <Route path="swap" element={<DiskSwap />} />
                </Route>
                <Route path="firewall" element={<Firewall />}>
                  <Route index element={<Navigate to="rules" replace />} />
                  <Route path="rules" element={<FirewallRules />} />
                  <Route path="ports" element={<FirewallPorts />} />
                  <Route path="fail2ban" element={<FirewallFail2ban />} />
                  <Route path="docker" element={<FirewallDocker />} />
                  <Route path="logs" element={<FirewallLogs />} />
                </Route>
                <Route path="packages" element={<Packages />} />
                <Route path="terminal" element={<Terminal />} />
                <Route path="settings" element={<Settings />} />
              </Route>
            </Routes>
          </Suspense>
        </SetupGuard>
      </TauriGuard>
      <Toaster />
    </BrowserRouter>
    </ErrorBoundary>
  )
}
