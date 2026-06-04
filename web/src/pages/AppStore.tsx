import { useState, useEffect, useCallback } from 'react'
import { useTranslation } from 'react-i18next'
import { useNavigate, useParams } from 'react-router-dom'
import { Search, RotateCcw, Loader2, Package, Info, ChevronDown, ChevronUp, X, AlertCircle, Star, ArrowDownUp, CheckCircle2 } from 'lucide-react'
import { toast } from 'sonner'
import { api } from '@/lib/api'
import { appStoreIconUrl } from '@/lib/appstore'
import { Input } from '@/components/ui/input'
import { Button } from '@/components/ui/button'
import AppStoreDetailModal from '@/pages/AppStoreDetail'
import type { AppStoreCategory, AppStoreApp } from '@/types/api'

export default function AppStore() {
  const { t, i18n } = useTranslation()
  const lang = i18n.language.startsWith('ko') ? 'ko' : 'en'
  const navigate = useNavigate()
  const { appId } = useParams<{ appId?: string }>()

  const [categories, setCategories] = useState<AppStoreCategory[]>([])
  const [apps, setApps] = useState<AppStoreApp[]>([])
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState(false)
  const [selectedCategory, setSelectedCategory] = useState('')
  const [search, setSearch] = useState('')
  const [sortBy, setSortBy] = useState<'featured' | 'name' | 'category'>('featured')
  const [installedOnly, setInstalledOnly] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const [stale, setStale] = useState(false)
  const [showGuide, setShowGuide] = useState(false)
  const [failedIcons, setFailedIcons] = useState<Set<string>>(new Set())
  const [updates, setUpdates] = useState<Record<string, boolean>>({})

  // The URL (/appstore/:appId) is the source of truth for which detail is open.
  const selectedAppId = appId ?? null

  const openApp = useCallback((id: string) => {
    navigate(`/appstore/${id}`)
  }, [navigate])

  const closeApp = useCallback(() => {
    navigate('/appstore')
  }, [navigate])

  const loadData = useCallback(async (category?: string) => {
    try {
      const [cats, appList] = await Promise.all([
        api.getAppStoreCategories(),
        api.getAppStoreApps(category || undefined),
      ])
      setCategories(cats)
      setApps(appList)
      setFailedIcons(new Set())
      setLoadError(false)
    } catch {
      setLoadError(true)
      toast.error(t('appStore.loadFailed'))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    loadData()
    api.getAppStoreStatus().then((s) => setStale(s.stale)).catch(() => {})
  }, [loadData])

  // Lazily check installed apps for available updates (best-effort, never throws).
  useEffect(() => {
    const installed = apps.filter((a) => a.installed)
    let cancelled = false
    ;(async () => {
      for (const a of installed) {
        try {
          const r = await api.checkStackUpdates(a.id)
          if (!cancelled && r.has_updates) setUpdates((m) => ({ ...m, [a.id]: true }))
        } catch {
          /* docker absent / not scanned — no badge */
        }
      }
    })()
    return () => { cancelled = true }
  }, [apps])

  const handleRetry = async () => {
    setLoading(true)
    await loadData(selectedCategory)
  }

  const handleCategoryClick = async (categoryId: string) => {
    setSelectedCategory(categoryId)
    setLoading(true)
    await loadData(categoryId)
  }

  const handleRefresh = async () => {
    setRefreshing(true)
    try {
      await api.refreshAppStore()
      toast.success(t('appStore.refreshSuccess'))
      setStale(false)
      await loadData(selectedCategory)
    } catch {
      toast.error(t('appStore.loadFailed'))
    } finally {
      setRefreshing(false)
    }
  }

  const filteredApps = apps
    .filter((app) => {
      if (installedOnly && !app.installed) return false
      if (!search) return true
      const q = search.toLowerCase()
      const desc = app.description[lang] || app.description['en'] || ''
      return app.name.toLowerCase().includes(q) || desc.toLowerCase().includes(q)
    })
    .sort((a, b) => {
      if (sortBy === 'name') return a.name.localeCompare(b.name)
      if (sortBy === 'category') {
        const cat = (a.category || '').localeCompare(b.category || '')
        return cat !== 0 ? cat : a.name.localeCompare(b.name)
      }
      // featured (default): featured first, then alphabetical by name
      if (!!a.featured !== !!b.featured) return a.featured ? -1 : 1
      return a.name.localeCompare(b.name)
    })

  return (
    <div className="space-y-6">
      {stale && (
        <div className="flex items-center gap-2 rounded-xl bg-amber-500/10 px-3 py-2 text-[13px] text-amber-600 ring-1 ring-amber-500/20">
          <AlertCircle className="h-4 w-4 shrink-0" />
          <span>{t('appStore.staleCatalog')}</span>
        </div>
      )}
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <h1 className="text-[22px] font-bold tracking-tight">{t('appStore.title')}</h1>
          <p className="text-[13px] text-muted-foreground mt-1">{t('appStore.subtitle')}</p>
        </div>
        <Button
          variant="outline"
          size="sm"
          className="rounded-xl"
          onClick={handleRefresh}
          disabled={refreshing}
        >
          {refreshing ? (
            <Loader2 className="h-4 w-4 animate-spin mr-2" />
          ) : (
            <RotateCcw className="h-4 w-4 mr-2" />
          )}
          {refreshing ? t('appStore.refreshing') : t('appStore.refresh')}
        </Button>
      </div>

      {/* How it works */}
      <div className="bg-card rounded-2xl card-shadow overflow-hidden">
        <button
          onClick={() => setShowGuide(!showGuide)}
          className="w-full flex items-center gap-2.5 px-4 py-3 text-left hover:bg-secondary/30 transition-colors"
        >
          <Info className="h-4 w-4 text-primary shrink-0" />
          <span className="text-[13px] font-medium flex-1">{t('appStore.guideTitle')}</span>
          {showGuide ? (
            <ChevronUp className="h-4 w-4 text-muted-foreground" />
          ) : (
            <ChevronDown className="h-4 w-4 text-muted-foreground" />
          )}
        </button>
        {showGuide && (
          <div className="px-4 pb-4 space-y-3 animate-in slide-in-from-top-1 duration-200">
            <div className="h-px bg-border" />
            <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
              {[
                { num: '1', title: t('appStore.guideStep1Title'), desc: t('appStore.guideStep1Desc') },
                { num: '2', title: t('appStore.guideStep2Title'), desc: t('appStore.guideStep2Desc') },
                { num: '3', title: t('appStore.guideStep3Title'), desc: t('appStore.guideStep3Desc') },
              ].map((step) => (
                <div key={step.num} className="flex gap-3">
                  <span className="inline-flex items-center justify-center h-5 w-5 rounded-full bg-primary/10 text-primary text-[11px] font-bold shrink-0 mt-0.5">
                    {step.num}
                  </span>
                  <div>
                    <p className="text-[12px] font-semibold">{step.title}</p>
                    <p className="text-[11px] text-muted-foreground mt-0.5 leading-relaxed">{step.desc}</p>
                  </div>
                </div>
              ))}
            </div>
            <div className="flex flex-wrap gap-x-4 gap-y-1 pt-1">
              <span className="text-[11px] text-muted-foreground">
                <span className="font-medium text-foreground">{t('appStore.guidePath')}</span> /opt/stacks/{'<app-id>'}/
              </span>
              <span className="text-[11px] text-muted-foreground">
                <span className="font-medium text-foreground">{t('appStore.guideFiles')}</span> docker-compose.yml, .env
              </span>
              <span className="text-[11px] text-muted-foreground">
                <span className="font-medium text-foreground">{t('appStore.guideManage')}</span> Docker Stacks
              </span>
            </div>
          </div>
        )}
      </div>

      {/* Shared search + sort/filter controls */}
      <div className="flex flex-wrap items-center justify-end gap-2">
        <button
          onClick={() => setInstalledOnly((v) => !v)}
          className={`inline-flex items-center gap-1.5 h-9 px-3 rounded-xl text-[13px] font-medium transition-colors ${
            installedOnly
              ? 'bg-[#00c471]/10 text-[#00c471]'
              : 'bg-secondary/50 text-muted-foreground hover:bg-secondary'
          }`}
        >
          <CheckCircle2 className="h-3.5 w-3.5" />
          {t('appStore.installedOnly')}
        </button>
        <div className="relative inline-flex items-center">
          <ArrowDownUp className="absolute left-3 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
          <select
            className="appearance-none h-9 pl-8 pr-8 rounded-xl bg-secondary/50 border-0 text-[13px] text-foreground cursor-pointer focus:outline-none focus:ring-1 focus:ring-primary/30"
            value={sortBy}
            onChange={(e) => setSortBy(e.target.value as 'featured' | 'name' | 'category')}
            aria-label={t('appStore.sortBy')}
          >
            <option value="featured">{t('appStore.sortFeatured')}</option>
            <option value="name">{t('appStore.sortName')}</option>
            <option value="category">{t('appStore.sortCategory')}</option>
          </select>
          <ChevronDown className="absolute right-2.5 h-3.5 w-3.5 text-muted-foreground pointer-events-none" />
        </div>
        <div className="relative flex items-center w-full sm:w-64">
          <Search className="absolute left-3 h-4 w-4 text-muted-foreground pointer-events-none" />
          <Input
            className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-[13px] w-full"
            placeholder={t('appStore.searchPlaceholder')}
            value={search}
            onChange={(e) => setSearch(e.target.value)}
          />
        </div>
      </div>

      {/* Category filter */}
      <div className="flex gap-2 flex-wrap">
        <button
          onClick={() => handleCategoryClick('')}
          className={`px-3 py-1.5 rounded-full text-[13px] font-medium transition-colors ${
            selectedCategory === ''
              ? 'bg-primary text-white'
              : 'bg-secondary/50 text-muted-foreground hover:bg-secondary'
          }`}
        >
          {t('appStore.all')}
        </button>
        {categories.map((cat) => (
          <button
            key={cat.id}
            onClick={() => handleCategoryClick(cat.id)}
            className={`px-3 py-1.5 rounded-full text-[13px] font-medium transition-colors ${
              selectedCategory === cat.id
                ? 'bg-primary text-white'
                : 'bg-secondary/50 text-muted-foreground hover:bg-secondary'
            }`}
          >
            {cat.name[lang] || cat.name['en']}
          </button>
        ))}
      </div>

      {/* App Grid */}
      {loading ? (
        <div className="flex items-center justify-center h-32">
          <div className="h-5 w-5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
        </div>
      ) : loadError ? (
        <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
          <AlertCircle className="h-10 w-10 mb-3 text-[#f04452] opacity-70" />
          <p className="text-[13px] mb-4">{t('appStore.loadFailed')}</p>
          <Button variant="outline" size="sm" className="rounded-xl" onClick={handleRetry}>
            <RotateCcw className="h-4 w-4 mr-2" />
            {t('appStore.retry')}
          </Button>
        </div>
      ) : filteredApps.length === 0 ? (
        search ? (
          <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
            <Search className="h-10 w-10 mb-3 opacity-40" />
            <p className="text-[13px] mb-4">{t('appStore.noResults', { query: search })}</p>
            <Button variant="outline" size="sm" className="rounded-xl" onClick={() => setSearch('')}>
              <X className="h-4 w-4 mr-2" />
              {t('appStore.clearSearch')}
            </Button>
          </div>
        ) : installedOnly ? (
          <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
            <CheckCircle2 className="h-10 w-10 mb-3 opacity-40" />
            <p className="text-[13px] mb-4">{t('appStore.noInstalledApps')}</p>
            <Button variant="outline" size="sm" className="rounded-xl" onClick={() => setInstalledOnly(false)}>
              <X className="h-4 w-4 mr-2" />
              {t('appStore.installedOnly')}
            </Button>
          </div>
        ) : (
          <div className="flex flex-col items-center justify-center py-16 text-muted-foreground">
            <Package className="h-10 w-10 mb-3 opacity-40" />
            <p className="text-[13px]">{t('appStore.noApps')}</p>
          </div>
        )
      ) : (
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-4">
          {filteredApps.map((app) => (
            <div
              key={app.id}
              onClick={() => openApp(app.id)}
              className="bg-card rounded-2xl p-5 card-shadow hover:card-shadow-hover cursor-pointer transition-all group"
            >
              <div className="flex items-start gap-4">
                <div className="h-12 w-12 rounded-xl bg-secondary/30 p-1.5 flex items-center justify-center overflow-hidden shrink-0 group-hover:scale-105 transition-transform">
                  {failedIcons.has(app.id) ? (
                    <div className="flex items-center justify-center h-full w-full text-primary">
                      <Package className="h-6 w-6" />
                    </div>
                  ) : (
                    <img
                      src={appStoreIconUrl(app.id, app.icon)}
                      alt={app.name}
                      className="h-full w-full object-contain"
                      onError={() => setFailedIcons(prev => new Set(prev).add(app.id))}
                    />
                  )}
                </div>
                <div className="flex-1 min-w-0">
                  <div className="flex items-center justify-between gap-2">
                    <div className="flex items-center gap-1.5 min-w-0">
                      <h3 className="text-[15px] font-semibold truncate">{app.name}</h3>
                      {app.featured && (
                        <span className="inline-flex items-center gap-0.5 px-1.5 py-0.5 rounded-full text-[10px] font-medium bg-amber-500/15 text-amber-600 dark:text-amber-400 shrink-0">
                          <Star className="h-2.5 w-2.5 fill-current" />
                          {t('appStore.featured')}
                        </span>
                      )}
                    </div>
                    {app.installed && updates[app.id] && (
                      <button
                        type="button"
                        onClick={(e) => { e.stopPropagation(); navigate(`/docker/stacks/${app.id}`) }}
                        className="inline-flex items-center gap-1 px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#3182f6]/10 text-[#3182f6]"
                      >
                        {t('appStore.updateAvailable')}
                      </button>
                    )}
                    {app.installed ? (
                      <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-[#00c471]/10 text-[#00c471] shrink-0">
                        {t('appStore.installed')}
                      </span>
                    ) : (
                      <span className="inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium bg-primary/10 text-primary shrink-0">
                        {t('appStore.install')}
                      </span>
                    )}
                  </div>
                  <p className="text-[12px] text-muted-foreground mt-1 line-clamp-2">
                    {app.description[lang] || app.description['en'] || ''}
                  </p>
                  <div className="flex items-center gap-2 mt-2">
                    <span className="text-[11px] text-muted-foreground">v{app.version}</span>
                    {app.ports.length > 0 && (
                      <span className="text-[11px] text-muted-foreground">
                        {t('appStore.port')}: {app.ports.join(', ')}
                      </span>
                    )}
                  </div>
                </div>
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Detail Modal */}
      <AppStoreDetailModal
        appId={selectedAppId}
        open={selectedAppId !== null}
        onClose={closeApp}
        onInstalled={() => loadData(selectedCategory)}
      />
    </div>
  )
}
