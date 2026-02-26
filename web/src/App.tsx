import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { BrowserRouter, Routes, Route, Navigate, useNavigate, useLocation } from 'react-router-dom'
import { Toaster } from '@/components/ui/sonner'
import { api } from '@/lib/api'
import Layout from '@/components/Layout'
import Login from '@/pages/Login'
import Dashboard from '@/pages/Dashboard'
import Docker from '@/pages/Docker'
import Files from '@/pages/Files'
import CronJobs from '@/pages/CronJobs'
import Logs from '@/pages/Logs'
import Processes from '@/pages/Processes'
import Network from '@/pages/Network'
import Packages from '@/pages/Packages'
import Settings from '@/pages/Settings'
import Terminal from '@/pages/Terminal'
import Setup from '@/pages/Setup'

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  if (!api.isAuthenticated()) {
    return <Navigate to="/login" replace />
  }
  return <>{children}</>
}

function SetupGuard({ children }: { children: React.ReactNode }) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const location = useLocation()
  const [checking, setChecking] = useState(true)

  useEffect(() => {
    // Skip check if already on the setup page
    if (location.pathname === '/setup') {
      setChecking(false)
      return
    }

    api.getSetupStatus()
      .then((data) => {
        if (data.setup_required) {
          navigate('/setup', { replace: true })
        }
      })
      .catch(() => {
        // If the endpoint fails, proceed normally
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

export default function App() {
  return (
    <BrowserRouter>
      <SetupGuard>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/setup" element={<Setup />} />
          <Route path="/" element={
            <ProtectedRoute>
              <Layout />
            </ProtectedRoute>
          }>
            <Route index element={<Navigate to="/dashboard" replace />} />
            <Route path="dashboard" element={<Dashboard />} />
            <Route path="docker" element={<Docker />} />
            <Route path="files" element={<Files />} />
            <Route path="cron" element={<CronJobs />} />
            <Route path="logs" element={<Logs />} />
            <Route path="processes" element={<Processes />} />
            <Route path="network" element={<Network />} />
            <Route path="packages" element={<Packages />} />
            <Route path="terminal" element={<Terminal />} />
            <Route path="settings" element={<Settings />} />
          </Route>
        </Routes>
      </SetupGuard>
      <Toaster />
    </BrowserRouter>
  )
}
