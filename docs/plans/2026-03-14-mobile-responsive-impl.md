# SFPanel Mobile Responsive UI/UX Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Transform SFPanel from a desktop-only panel into a mobile-first responsive app with bottom navigation, card-based views, and PWA support.

**Architecture:** Tailwind `md:` breakpoint (768px) splits mobile/desktop rendering. Mobile gets BottomNav + MobileHeader; desktop keeps existing sidebar. Pages use conditional rendering (`md:hidden` / `hidden md:block`) to show cards on mobile, tables on desktop. PWA via vite-plugin-pwa for installable app experience.

**Tech Stack:** React 19, Tailwind CSS v4, shadcn/ui Drawer (vaul), vite-plugin-pwa, lucide-react icons

**Design Doc:** `docs/plans/2026-03-14-mobile-responsive-design.md`

---

## Phase 1: Foundation Infrastructure

### Task 1: Install dependencies (vaul drawer + vite-plugin-pwa)

**Files:**
- Modify: `web/package.json`
- Modify: `web/vite.config.ts`

**Step 1: Install vaul (drawer/bottom sheet) and vite-plugin-pwa**

```bash
cd /opt/stacks/SFPanel/web
npm install vaul vite-plugin-pwa
```

**Step 2: Verify installation**

```bash
cd /opt/stacks/SFPanel/web
node -e "require('vaul'); require('vite-plugin-pwa'); console.log('OK')"
```
Expected: `OK`

**Step 3: Commit**

```bash
git add web/package.json web/package-lock.json
git commit -m "deps: vaul(drawer) + vite-plugin-pwa 설치"
```

---

### Task 2: Add safe-area CSS utilities and mobile viewport meta

**Files:**
- Modify: `web/src/index.css`
- Modify: `web/index.html`

**Step 1: Add safe-area and mobile utilities to index.css**

Add these utilities inside the existing `@layer utilities` block in `web/src/index.css`:

```css
/* Add inside the existing @layer utilities { ... } block, after .card-shadow-lg */

.pb-safe {
  padding-bottom: env(safe-area-inset-bottom, 0px);
}

.bottom-safe {
  bottom: env(safe-area-inset-bottom, 0px);
}

/* Bottom nav height: 56px + safe area */
.pb-bottom-nav {
  padding-bottom: calc(56px + env(safe-area-inset-bottom, 0px));
}
```

**Step 2: Update web/index.html meta tags for mobile**

Add viewport-fit=cover to the existing viewport meta tag and add apple-mobile-web-app tags. Current `web/index.html` has a standard viewport meta. Replace it:

```html
<!-- Replace existing viewport meta -->
<meta name="viewport" content="width=device-width, initial-scale=1.0, viewport-fit=cover" />

<!-- Add these new meta tags in <head> -->
<meta name="apple-mobile-web-app-capable" content="yes" />
<meta name="apple-mobile-web-app-status-bar-style" content="default" />
<meta name="theme-color" content="#3182f6" />
<link rel="manifest" href="/manifest.json" />
```

**Step 3: Verify build**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```
Expected: Build succeeds without errors.

**Step 4: Commit**

```bash
git add web/src/index.css web/index.html
git commit -m "feat: safe-area CSS 유틸리티 + 모바일 viewport 메타 태그"
```

---

### Task 3: Create PWA manifest and configure vite-plugin-pwa

**Files:**
- Create: `web/public/manifest.json`
- Modify: `web/vite.config.ts`

**Step 1: Create manifest.json**

Create `web/public/manifest.json`:

```json
{
  "name": "SFPanel",
  "short_name": "SFPanel",
  "description": "Server Management Panel",
  "start_url": "/dashboard",
  "display": "standalone",
  "background_color": "#f7f8fa",
  "theme_color": "#3182f6",
  "icons": [
    {
      "src": "/icon-192.png",
      "sizes": "192x192",
      "type": "image/png"
    },
    {
      "src": "/icon-512.png",
      "sizes": "512x512",
      "type": "image/png"
    },
    {
      "src": "/icon-512-maskable.png",
      "sizes": "512x512",
      "type": "image/png",
      "purpose": "maskable"
    }
  ]
}
```

**Step 2: Generate placeholder PWA icons**

We need 192x192 and 512x512 PNG icons. For now, create simple SVG-based placeholders that can be replaced later with proper branding:

```bash
cd /opt/stacks/SFPanel/web/public

# Create a simple SVG icon and convert to PNG using a canvas approach
# For now, copy the existing vite.svg as placeholder (will be replaced)
# The actual icons should be created by a designer or generated from the SFPanel logo
```

Note: PWA icons (`icon-192.png`, `icon-512.png`, `icon-512-maskable.png`) need to be created separately. For now the manifest references them; the app will work without them but won't be installable until they exist. Create simple placeholder PNGs with the text "SF" on a blue (#3182f6) background.

**Step 3: Configure vite-plugin-pwa in vite.config.ts**

Current `web/vite.config.ts` structure:

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'
```

Add the PWA plugin. The full updated config:

```ts
import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import { VitePWA } from 'vite-plugin-pwa'
import path from 'path'

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    VitePWA({
      registerType: 'prompt',
      workbox: {
        globPatterns: ['**/*.{js,css,html,ico,png,svg,woff2}'],
        navigateFallback: '/index.html',
        runtimeCaching: [
          {
            urlPattern: /^\/api\//,
            handler: 'NetworkFirst',
            options: {
              cacheName: 'api-cache',
              expiration: { maxEntries: 50, maxAgeSeconds: 300 },
            },
          },
        ],
      },
    }),
  ],
  // ... rest of existing config (resolve, server proxy, etc.)
})
```

Important: Keep all existing config (resolve.alias, server.proxy) intact. Only add the `VitePWA` plugin and its import.

**Step 4: Verify build**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```
Expected: Build succeeds. Check `web/dist/` for `sw.js` or `registerSW.js`.

**Step 5: Commit**

```bash
git add web/public/manifest.json web/vite.config.ts
git commit -m "feat: PWA 매니페스트 + Service Worker 설정"
```

---

### Task 4: Create useIsMobile hook

**Files:**
- Create: `web/src/hooks/useIsMobile.ts`

**Step 1: Create the hook**

Create `web/src/hooks/useIsMobile.ts`:

```tsx
import { useState, useEffect } from 'react'

const MOBILE_BREAKPOINT = 768

export function useIsMobile(): boolean {
  const [isMobile, setIsMobile] = useState(() => window.innerWidth < MOBILE_BREAKPOINT)

  useEffect(() => {
    const mql = window.matchMedia(`(max-width: ${MOBILE_BREAKPOINT - 1}px)`)
    const onChange = (e: MediaQueryListEvent) => setIsMobile(e.matches)
    mql.addEventListener('change', onChange)
    return () => mql.removeEventListener('change', onChange)
  }, [])

  return isMobile
}
```

**Step 2: Verify build**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```
Expected: Build succeeds (unused export is fine).

**Step 3: Commit**

```bash
git add web/src/hooks/useIsMobile.ts
git commit -m "feat: useIsMobile 훅 생성 (768px 브레이크포인트)"
```

---

### Task 5: Create BottomNav component

**Files:**
- Create: `web/src/components/BottomNav.tsx`
- Modify: `web/src/i18n/locales/ko.json` (add mobile nav keys)
- Modify: `web/src/i18n/locales/en.json` (add mobile nav keys)

**Step 1: Add i18n keys for mobile navigation**

Add these keys to `web/src/i18n/locales/ko.json` inside the `"layout"` object:

```json
"mobileNav": {
  "dashboard": "대시보드",
  "docker": "Docker",
  "terminal": "터미널",
  "processes": "프로세스",
  "more": "더보기"
}
```

Add to `web/src/i18n/locales/en.json` inside the `"layout"` object:

```json
"mobileNav": {
  "dashboard": "Dashboard",
  "docker": "Docker",
  "terminal": "Terminal",
  "processes": "Processes",
  "more": "More"
}
```

**Step 2: Create BottomNav.tsx**

Create `web/src/components/BottomNav.tsx`:

```tsx
import { NavLink, useLocation } from 'react-router-dom'
import { LayoutDashboard, Container, Terminal, Activity, Menu, Server } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { useState, useEffect } from 'react'
import { api } from '@/lib/api'
import type { ClusterStatus } from '@/types/api'
import { cn } from '@/lib/utils'

interface BottomNavProps {
  onMorePress: () => void
}

export default function BottomNav({ onMorePress }: BottomNavProps) {
  const { t } = useTranslation()
  const location = useLocation()
  const [clusterEnabled, setClusterEnabled] = useState(false)

  useEffect(() => {
    api.getClusterStatus(true)
      .then((status: ClusterStatus) => setClusterEnabled(status.enabled))
      .catch(() => {})
  }, [])

  const tabs = [
    { to: '/dashboard', icon: LayoutDashboard, label: t('layout.mobileNav.dashboard') },
    { to: '/docker', icon: Container, label: t('layout.mobileNav.docker') },
    { to: '/terminal', icon: Terminal, label: t('layout.mobileNav.terminal') },
    // 4th tab: context-aware (processes for single node, node selector for cluster)
    clusterEnabled
      ? { to: '/cluster', icon: Server, label: t('layout.nav.cluster') }
      : { to: '/processes', icon: Activity, label: t('layout.mobileNav.processes') },
    { to: '#more', icon: Menu, label: t('layout.mobileNav.more'), isMore: true },
  ]

  // Hide on terminal page (fullscreen mode)
  if (location.pathname === '/terminal') return null

  return (
    <nav className="fixed bottom-0 left-0 right-0 z-50 bg-card border-t border-border md:hidden pb-safe">
      <div className="flex items-center justify-around h-14">
        {tabs.map((tab) => {
          if (tab.isMore) {
            return (
              <button
                key="more"
                onClick={onMorePress}
                className="flex flex-col items-center justify-center gap-0.5 flex-1 h-full text-muted-foreground active:opacity-70 transition-opacity"
              >
                <tab.icon className="h-[22px] w-[22px]" />
                <span className="text-[10px] font-medium">{tab.label}</span>
              </button>
            )
          }

          return (
            <NavLink
              key={tab.to}
              to={tab.to}
              className={({ isActive }) =>
                cn(
                  'flex flex-col items-center justify-center gap-0.5 flex-1 h-full transition-colors active:opacity-70',
                  isActive || location.pathname.startsWith(tab.to)
                    ? 'text-[#3182f6]'
                    : 'text-muted-foreground'
                )
              }
            >
              <tab.icon className="h-[22px] w-[22px]" />
              <span className="text-[10px] font-medium">{tab.label}</span>
            </NavLink>
          )
        })}
      </div>
    </nav>
  )
}
```

**Step 3: Verify build**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```
Expected: Build succeeds.

**Step 4: Commit**

```bash
git add web/src/components/BottomNav.tsx web/src/i18n/locales/ko.json web/src/i18n/locales/en.json
git commit -m "feat: BottomNav 컴포넌트 (5탭 바텀 네비게이션)"
```

---

### Task 6: Create MobileHeader component

**Files:**
- Create: `web/src/components/MobileHeader.tsx`

**Step 1: Create MobileHeader.tsx**

Create `web/src/components/MobileHeader.tsx`:

```tsx
import { ReactNode } from 'react'

interface MobileHeaderProps {
  title: string
  actions?: ReactNode
}

export default function MobileHeader({ title, actions }: MobileHeaderProps) {
  return (
    <header className="sticky top-0 z-40 bg-background/80 backdrop-blur-sm border-b border-border md:hidden">
      <div className="flex items-center justify-between h-11 px-4">
        <h1 className="text-[15px] font-semibold truncate">{title}</h1>
        {actions && <div className="flex items-center gap-1 shrink-0">{actions}</div>}
      </div>
    </header>
  )
}
```

**Step 2: Verify build**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```
Expected: Build succeeds.

**Step 3: Commit**

```bash
git add web/src/components/MobileHeader.tsx
git commit -m "feat: MobileHeader 컴포넌트 (모바일 상단 헤더)"
```

---

### Task 7: Create BottomSheet (More Menu) component

**Files:**
- Create: `web/src/components/MoreMenu.tsx`

**Step 1: Create MoreMenu.tsx using vaul Drawer**

Create `web/src/components/MoreMenu.tsx`:

```tsx
import { useNavigate, useLocation } from 'react-router-dom'
import { Drawer } from 'vaul'
import {
  FolderOpen, Clock, FileText, Package, Settings, Cog,
  Network, HardDrive, Shield, Store, Server, LogOut, Activity,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { api } from '@/lib/api'
import { cn } from '@/lib/utils'

interface MoreMenuProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const menuItems = [
  { to: '/processes', labelKey: 'layout.nav.processes', icon: Activity },
  { to: '/cluster', labelKey: 'layout.nav.cluster', icon: Server },
  { to: '/appstore', labelKey: 'layout.nav.appstore', icon: Store },
  { to: '/files', labelKey: 'layout.nav.files', icon: FolderOpen },
  { to: '/cron', labelKey: 'layout.nav.cron', icon: Clock },
  { to: '/logs', labelKey: 'layout.nav.logs', icon: FileText },
  { to: '/services', labelKey: 'layout.nav.services', icon: Cog },
  { to: '/network', labelKey: 'layout.nav.networkVpn', icon: Network },
  { to: '/disk', labelKey: 'layout.nav.disk', icon: HardDrive },
  { to: '/firewall', labelKey: 'layout.nav.firewall', icon: Shield },
  { to: '/packages', labelKey: 'layout.nav.packages', icon: Package },
  { to: '/settings', labelKey: 'layout.nav.settings', icon: Settings },
]

export default function MoreMenu({ open, onOpenChange }: MoreMenuProps) {
  const navigate = useNavigate()
  const location = useLocation()
  const { t } = useTranslation()

  const handleNav = (to: string) => {
    navigate(to)
    onOpenChange(false)
  }

  const handleLogout = () => {
    api.clearToken()
    navigate('/login')
    onOpenChange(false)
  }

  return (
    <Drawer.Root open={open} onOpenChange={onOpenChange}>
      <Drawer.Portal>
        <Drawer.Overlay className="fixed inset-0 bg-black/40 z-50" />
        <Drawer.Content className="fixed bottom-0 left-0 right-0 z-50 bg-card rounded-t-2xl outline-none">
          <div className="mx-auto w-12 h-1.5 rounded-full bg-muted-foreground/20 mt-3 mb-2" />
          <Drawer.Title className="sr-only">{t('layout.mobileNav.more')}</Drawer.Title>
          <div className="px-2 pb-safe max-h-[70vh] overflow-y-auto">
            <div className="grid grid-cols-4 gap-1 px-2 pb-4">
              {menuItems.map((item) => {
                const isActive = location.pathname.startsWith(item.to)
                return (
                  <button
                    key={item.to}
                    onClick={() => handleNav(item.to)}
                    className={cn(
                      'flex flex-col items-center gap-1.5 py-3 rounded-xl transition-colors active:opacity-70',
                      isActive ? 'bg-primary/10 text-[#3182f6]' : 'text-foreground hover:bg-accent'
                    )}
                  >
                    <item.icon className="h-[22px] w-[22px]" />
                    <span className="text-[11px] font-medium">{t(item.labelKey)}</span>
                  </button>
                )
              })}
            </div>
            <div className="border-t border-border mx-2 pt-2 pb-3">
              <button
                onClick={handleLogout}
                className="flex items-center gap-3 w-full px-4 py-3 rounded-xl text-[13px] font-medium text-muted-foreground hover:bg-accent transition-colors active:opacity-70"
              >
                <LogOut className="h-[18px] w-[18px]" />
                {t('layout.logout')}
              </button>
            </div>
          </div>
        </Drawer.Content>
      </Drawer.Portal>
    </Drawer.Root>
  )
}
```

**Step 2: Verify build**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```
Expected: Build succeeds.

**Step 3: Commit**

```bash
git add web/src/components/MoreMenu.tsx
git commit -m "feat: MoreMenu 컴포넌트 (바텀시트 더보기 메뉴)"
```

---

### Task 8: Update Layout.tsx for mobile responsive

**Files:**
- Modify: `web/src/components/Layout.tsx`

This is the core task. Layout.tsx must:
1. Hide sidebar on mobile (`hidden md:flex`)
2. Add BottomNav on mobile
3. Add MoreMenu drawer
4. Adjust main content padding for mobile
5. Add bottom padding for BottomNav clearance

**Step 1: Update Layout.tsx**

The current Layout.tsx is 179 lines. Here are the specific changes:

**Add imports** (at top of file, after existing imports):

```tsx
import BottomNav from '@/components/BottomNav'
import MoreMenu from '@/components/MoreMenu'
```

**Add state for MoreMenu** (inside the Layout component function, after existing state):

```tsx
const [moreOpen, setMoreOpen] = useState(false)
```

**Modify the sidebar `<aside>` element** — add `hidden md:flex` to hide on mobile:

Change line 67-70 from:
```tsx
<aside className={cn(
  'bg-card border-r border-border flex flex-col transition-all duration-300 ease-in-out shrink-0',
  collapsed ? 'w-[68px]' : 'w-60'
)}>
```
To:
```tsx
<aside className={cn(
  'bg-card border-r border-border flex-col transition-all duration-300 ease-in-out shrink-0 hidden md:flex',
  collapsed ? 'w-[68px]' : 'w-60'
)}>
```

**Modify `<main>` padding** — reduce on mobile, add bottom padding for BottomNav:

Change line 174 from:
```tsx
<main className="flex-1 overflow-auto p-8">
```
To:
```tsx
<main className="flex-1 overflow-auto p-4 pb-bottom-nav md:p-8 md:pb-8">
```

**Add BottomNav and MoreMenu after `</main>`** (before the closing `</div>` of the root flex container):

```tsx
      <BottomNav onMorePress={() => setMoreOpen(true)} />
      <MoreMenu open={moreOpen} onOpenChange={setMoreOpen} />
```

**Step 2: Verify build**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```
Expected: Build succeeds.

**Step 3: Test in browser**

Open browser, resize to < 768px width:
- Sidebar should be hidden
- Bottom navigation bar with 5 tabs should appear
- "More" tab should open a drawer with all menu items
- Clicking menu items should navigate and close drawer
- At >= 768px, sidebar should reappear, bottom nav should be hidden

**Step 4: Commit**

```bash
git add web/src/components/Layout.tsx
git commit -m "feat: Layout 모바일 반응형 — 사이드바 hidden md:flex, BottomNav 연결"
```

---

## Phase 2: Tier 1 Core Pages

### Task 9: Dashboard mobile view

**Files:**
- Modify: `web/src/pages/Dashboard.tsx`

The Dashboard currently uses grid layouts for metrics cards, charts, and system info tables. For mobile:
1. Metrics cards: Force 1-column on mobile (`grid-cols-1 sm:grid-cols-2 lg:grid-cols-4`)
2. Quick actions: 1 row horizontal scroll on mobile
3. Charts: Allow horizontal scroll on mobile
4. System info tables: Stack vertically, reduce padding
5. Process/Container/Firewall sections: Simplify for mobile

**Step 1: Examine current Dashboard grid patterns**

Read `web/src/pages/Dashboard.tsx` fully to identify all grid containers. The key patterns to change:

- Metrics cards grid: Change to `grid-cols-2 sm:grid-cols-2 lg:grid-cols-4` (2-col on mobile for compact view)
- Quick actions: Change to `flex overflow-x-auto gap-2 md:grid md:grid-cols-5` (horizontal scroll on mobile)
- Chart container: Add `overflow-x-auto` wrapper
- System info card: Keep as-is (already uses key-value pairs, not table)
- Top processes table: On mobile, show simplified card list
- Containers summary table: On mobile, show simplified card list

**Step 2: Update Dashboard.tsx grids**

Key changes (apply to the specific grid sections in Dashboard.tsx):

For metrics cards (find the `grid grid-cols-2 lg:grid-cols-4` section):
```tsx
{/* Metrics cards - already uses grid-cols-2, just ensure gap is responsive */}
<div className="grid grid-cols-2 lg:grid-cols-4 gap-3 md:gap-4">
```

For quick actions (find the grid section with quickActions):
```tsx
{/* Quick actions - horizontal scroll on mobile, grid on desktop */}
<div className="flex gap-2 overflow-x-auto md:grid md:grid-cols-5 md:gap-3 pb-1 md:pb-0 -mx-1 px-1 md:mx-0 md:px-0">
```
Each action card should have `shrink-0 w-[120px] md:w-auto` on mobile.

For system info and other sections, reduce padding:
```tsx
<div className="bg-card rounded-2xl p-4 md:p-6 card-shadow">
```

For top processes table on mobile — wrap with overflow-x-auto:
```tsx
<div className="bg-card rounded-2xl card-shadow overflow-hidden overflow-x-auto">
```

For firewall log and container summary tables — same overflow-x-auto treatment.

**Step 3: Verify build and test**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```

Test: Open dashboard on mobile width (375px). Cards should be 2-column, quick actions horizontal scroll, tables should scroll horizontally.

**Step 4: Commit**

```bash
git add web/src/pages/Dashboard.tsx
git commit -m "feat: 대시보드 모바일 반응형 (카드 그리드, 퀵액션 스크롤)"
```

---

### Task 10: Docker Containers mobile card view

**Files:**
- Modify: `web/src/pages/docker/DockerContainers.tsx`
- Modify: `web/src/i18n/locales/ko.json`
- Modify: `web/src/i18n/locales/en.json`

This is the most complex mobile conversion. The containers page has a table with columns: checkbox, name, image, status, resources, ports, created, actions. On mobile, each container becomes a card.

**Step 1: Add mobile card rendering**

In DockerContainers.tsx, the main rendering section has a `<Table>` component. We need to add a mobile card view that shows alongside a hidden-on-mobile table.

The approach: Keep the existing table but wrap it with `hidden md:block`. Add a new mobile card list with `md:hidden`.

**Mobile card structure per container:**

```tsx
{/* Mobile card view */}
<div className="space-y-2 md:hidden">
  {containers.map((c) => (
    <div key={c.Id} className="bg-card rounded-2xl p-4 card-shadow">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          <p className="text-[13px] font-semibold truncate">{c.Names?.[0]?.replace(/^\//, '') || c.Id.slice(0, 12)}</p>
          <p className="text-[11px] text-muted-foreground truncate mt-0.5">{c.Image}</p>
        </div>
        <span className={cn(
          'inline-flex items-center px-2 py-0.5 rounded-full text-[11px] font-medium shrink-0',
          getStatusStyle(c.State)
        )}>
          {c.State}
        </span>
      </div>
      {/* Ports */}
      {c.Ports && c.Ports.length > 0 && (
        <p className="text-[11px] text-muted-foreground font-mono mt-2 truncate">
          {formatPorts(c.Ports)}
        </p>
      )}
      {/* Actions */}
      <div className="flex items-center gap-1.5 mt-3 justify-end">
        {/* Same action buttons as table row, but icon-only with size="icon-xs" */}
        {/* start/stop, restart, logs, shell, delete */}
      </div>
    </div>
  ))}
</div>
```

**Desktop table**: Wrap the existing `<div className="bg-card rounded-2xl card-shadow overflow-hidden overflow-x-auto">` table container with an additional `hidden md:block`:

```tsx
<div className="hidden md:block">
  <div className="bg-card rounded-2xl card-shadow overflow-hidden overflow-x-auto">
    <Table>...</Table>
  </div>
</div>
```

**Stack headers on mobile** — the stack grouping sections should work the same way on mobile, but show cards instead of table rows inside each stack group.

**Step 2: Implement the mobile card rendering logic**

The mobile card view should reuse the same data (`containers` array), status functions (`getStatusStyle`), and action handlers (`handleAction`, etc.) already defined in the component.

For stack groups on mobile, keep the same collapsible stack header (ChevronRight + Layers icon + stack name), but render cards below instead of table rows.

**Step 3: Verify build and test**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```

Test at 375px width: Each container should display as a card with name, image, status pill, ports, and action buttons.

**Step 4: Commit**

```bash
git add web/src/pages/docker/DockerContainers.tsx web/src/i18n/locales/ko.json web/src/i18n/locales/en.json
git commit -m "feat: Docker 컨테이너 모바일 카드뷰"
```

---

### Task 11: Docker Stacks mobile view

**Files:**
- Modify: `web/src/pages/docker/DockerStacks.tsx`

**Step 1: Read current DockerStacks.tsx**

Read `web/src/pages/docker/DockerStacks.tsx` to understand its current structure (table of stacks with compose editor).

**Step 2: Mobile optimizations**

1. Stack list: Wrap existing table with `hidden md:block`, add mobile card list with `md:hidden`
2. Compose editor dialog: Make fullscreen on mobile by adding responsive classes to DialogContent

Mobile stack card:
```tsx
<div className="bg-card rounded-2xl p-4 card-shadow">
  <div className="flex items-center justify-between">
    <div className="min-w-0 flex-1">
      <p className="text-[13px] font-semibold truncate">{stack.name}</p>
      <p className="text-[11px] text-muted-foreground mt-0.5">
        {stack.container_count} containers
      </p>
    </div>
    <span className={statusPill}>{stack.status}</span>
  </div>
  <div className="flex gap-1.5 mt-3 justify-end">
    {/* up/down/restart/edit/delete buttons */}
  </div>
</div>
```

Compose editor dialog — make fullscreen on mobile:
```tsx
<DialogContent className="max-w-4xl w-full h-[90vh] md:h-[80vh] max-h-[90vh] md:max-h-[80vh] p-0 md:p-6">
```

**Step 3: Verify build and test**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```

**Step 4: Commit**

```bash
git add web/src/pages/docker/DockerStacks.tsx
git commit -m "feat: Docker 스택 모바일 카드뷰 + 에디터 풀스크린"
```

---

### Task 12: Terminal fullscreen mobile mode

**Files:**
- Modify: `web/src/pages/Terminal.tsx`

**Step 1: Read current Terminal.tsx**

Read `web/src/pages/Terminal.tsx` fully.

**Step 2: Mobile optimizations**

Terminal is already mostly fullscreen. Key changes:
1. Tab bar: Horizontal scroll with `overflow-x-auto` on mobile
2. Controls: Minimize font size controls to icon-only on mobile
3. Terminal area: Remove padding on mobile for max space
4. BottomNav is already hidden on `/terminal` (handled in BottomNav component)

Tab bar change:
```tsx
{/* Tab bar */}
<div className="flex items-center gap-1 overflow-x-auto px-2 md:px-4 py-1">
```

Controls change — hide labels on mobile:
```tsx
<div className="flex items-center gap-1 px-2 md:px-4">
  {/* Font size controls - smaller on mobile */}
  <Button size="icon-xs" ...>
```

Terminal container — reduce padding:
```tsx
<div className="flex-1 p-1 md:p-2">
```

**Step 3: Verify build and test**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```

Test: Terminal should use full viewport on mobile, bottom nav should be hidden, tabs should scroll.

**Step 4: Commit**

```bash
git add web/src/pages/Terminal.tsx
git commit -m "feat: 터미널 모바일 풀스크린 모드"
```

---

### Task 13: Processes mobile card view

**Files:**
- Modify: `web/src/pages/Processes.tsx`

**Step 1: Read current Processes.tsx**

Read `web/src/pages/Processes.tsx` fully. It uses virtual scrolling with `@tanstack/react-virtual`.

**Step 2: Mobile card view**

The processes page uses a virtualized table with ROW_HEIGHT = 44. For mobile, we need a card-based virtualized list.

Approach: Use the same virtualizer but with different row heights and card rendering on mobile.

```tsx
// Detect mobile via CSS class toggle (not hook, to keep virtualizer simple)
// Use md:hidden / hidden md:block pattern for table vs card rendering

{/* Mobile card view */}
<div className="md:hidden" ref={scrollRef} style={{ height: '60vh', overflow: 'auto' }}>
  <div style={{ height: `${rowVirtualizer.getTotalSize()}px`, position: 'relative' }}>
    {rowVirtualizer.getVirtualItems().map((virtualRow) => {
      const proc = filtered[virtualRow.index]
      return (
        <div
          key={proc.pid}
          style={{ position: 'absolute', top: virtualRow.start, width: '100%', height: virtualRow.size }}
          className="px-1"
        >
          <div className="bg-card rounded-xl p-3 card-shadow flex items-center justify-between">
            <div className="min-w-0 flex-1">
              <p className="text-[13px] font-medium truncate">{proc.name}</p>
              <div className="flex items-center gap-3 mt-1 text-[11px] text-muted-foreground">
                <span>PID {proc.pid}</span>
                <span className="flex items-center gap-1"><Cpu className="h-3 w-3" />{proc.cpu.toFixed(1)}%</span>
                <span className="flex items-center gap-1"><MemoryStick className="h-3 w-3" />{formatBytes(proc.memory)}</span>
              </div>
            </div>
            <Button size="icon-xs" variant="ghost" onClick={() => setKillTarget(proc)}>
              <Skull className="h-4 w-4 text-destructive" />
            </Button>
          </div>
        </div>
      )
    })}
  </div>
</div>
```

Note: The virtualizer needs different row heights for mobile (64px card) vs desktop (44px table row). Use the `useIsMobile` hook to set row height:

```tsx
import { useIsMobile } from '@/hooks/useIsMobile'

// Inside component:
const isMobile = useIsMobile()
const ROW_H = isMobile ? 68 : ROW_HEIGHT
```

Then update virtualizer:
```tsx
const rowVirtualizer = useVirtualizer({
  count: filtered.length,
  getScrollElement: () => scrollRef.current,
  estimateSize: () => ROW_H,
  overscan: 10,
})
```

**Step 3: Handle metrics cards at top**

The processes page has 3 system metrics cards at the top (CPU, Memory, Disk). Ensure they stack well:
```tsx
<div className="grid grid-cols-3 gap-2 md:gap-4">
```

**Step 4: Search and sort controls**

Make search and sort responsive:
```tsx
<div className="flex flex-col sm:flex-row items-stretch sm:items-center gap-2">
```

**Step 5: Verify build and test**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```

Test at 375px: Processes should show as cards with name, PID, CPU%, MEM, and kill button.

**Step 6: Commit**

```bash
git add web/src/pages/Processes.tsx
git commit -m "feat: 프로세스 모바일 카드뷰 (가상화 유지)"
```

---

### Task 14: Add PWA icon placeholders

**Files:**
- Create: `web/public/icon-192.png`
- Create: `web/public/icon-512.png`
- Create: `web/public/icon-512-maskable.png`

**Step 1: Generate simple PWA icons**

Use a Node.js script or canvas to generate simple blue icons with "SF" text. If this is not easily scriptable, create them manually using any image editor.

Alternative: Use a simple SVG approach. Create an SVG and use sharp/canvas to convert:

```bash
cd /opt/stacks/SFPanel/web/public

# Install sharp temporarily for icon generation (or create manually)
# Create a simple script to generate icons:
node -e "
const { createCanvas } = require('canvas');
// ... generate icons
"
```

If canvas is not available, create the icons manually as PNG files with:
- Blue (#3182f6) background
- White "SF" text centered
- Sizes: 192x192, 512x512, 512x512 (with extra padding for maskable)

For now, you can also skip this step and create the icons later — the PWA will still work but won't be installable.

**Step 2: Commit**

```bash
git add web/public/icon-*.png
git commit -m "feat: PWA 아이콘 추가 (192/512px)"
```

---

### Task 15: Final Phase 1+2 integration test and commit

**Files:**
- No new files

**Step 1: Full build**

```bash
cd /opt/stacks/SFPanel/web && npm run build
```
Expected: Clean build with no errors.

**Step 2: Check built output**

```bash
ls -la /opt/stacks/SFPanel/web/dist/
# Should contain: index.html, assets/, manifest.json, sw.js (or similar)
```

**Step 3: Browser testing checklist**

Open the app in a browser. Test at different widths:

**Mobile (375px):**
- [ ] Sidebar hidden
- [ ] Bottom nav visible with 5 tabs
- [ ] Dashboard: 2-column metric cards, horizontal quick actions
- [ ] Docker Containers: Card list, stack groups work
- [ ] Docker Stacks: Card list, editor opens fullscreen
- [ ] Terminal: Fullscreen, bottom nav hidden
- [ ] Processes: Virtualized card list
- [ ] "More" tab opens drawer with all menu items
- [ ] Navigation works from all tabs and drawer items

**Desktop (1200px):**
- [ ] Sidebar visible (no changes from before)
- [ ] Bottom nav hidden
- [ ] All pages look the same as before
- [ ] No regressions

**Step 4: Commit any fixes**

```bash
git add -A
git commit -m "fix: Phase 1+2 모바일 반응형 통합 수정"
```

---

## Phase 3: Tier 2 Pages (Overview)

> Phase 3 is outlined here for planning but will be implemented in a subsequent plan document.

### Task 16 (Future): Services mobile card view
- Table → card list (name + status pill + start/stop/restart dropdown)

### Task 17 (Future): File manager mobile view
- File list as cards, editor in fullscreen modal

### Task 18 (Future): Log viewer mobile view
- Source selector as dropdown (not sidebar), full-width viewer

### Task 19 (Future): Firewall tables mobile
- Add `overflow-x-auto` to all table wrappers, hide less critical columns with `hidden md:table-cell`

### Task 20 (Future): Cluster mobile view
- Node cards, metrics single column

---

## Phase 4: Tier 3 + Polish (Overview)

> Phase 4 is outlined here for planning but will be implemented in a subsequent plan document.

### Task 21 (Future): Remaining pages overflow-x-auto
- Network, Disk, Cron pages: wrap tables with `overflow-x-auto`, reduce padding

### Task 22 (Future): Pull-to-refresh
- Dashboard, Docker, Processes: overscroll-behavior-y + touch event detection

### Task 23 (Future): Swipe actions on cards
- Container/service cards: swipe left to reveal stop/delete actions

### Task 24 (Future): Terminal keyboard handling
- Detect soft keyboard open, adjust terminal height, hide bottom nav

---

## Key Implementation Notes

### CSS Strategy
- Use Tailwind `md:` prefix (768px) as the single breakpoint
- `md:hidden` = visible only on mobile
- `hidden md:block` = visible only on desktop
- `hidden md:table-cell` = table column visible only on desktop
- Don't create separate mobile components — use conditional rendering within existing pages

### Performance
- No new API calls — mobile views use the same data as desktop
- Virtual scrolling preserved for Processes (just different row height)
- Lazy loading already in place for all pages

### Testing Strategy
- Use Playwright (MCP browser tools) for testing
- Test at 375px (iPhone SE), 430px (iPhone 15 Pro Max), and 768px (iPad Mini)
- Verify no desktop regressions at 1200px+

### Design System Compliance
- Cards: `bg-card rounded-2xl p-4 card-shadow` (smaller padding on mobile)
- Status pills: Same `rounded-full text-[11px]` pattern
- Touch targets: Min 44px height for all interactive elements
- Colors: Same palette (#3182f6, #00c471, #f04452, #f59e0b)
