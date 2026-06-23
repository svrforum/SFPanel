import { Component, type ReactNode } from 'react'
import React from 'react'
import { LANGUAGE_KEY } from '../i18n'

interface Props {
    children: ReactNode
}

interface State {
    hasError: boolean
    error: Error | null
}

// The boundary renders outside the i18n React context (a crashed tree can't
// use useTranslation), so replicate the detector's resolution manually:
// stored preference first, then the browser language, defaulting to English
// (i18n fallbackLng). Without this the crash screen was hardcoded Korean for
// every user.
function crashCopy(): { title: string; body: string; reload: string } {
    let lng = ''
    try {
        lng = localStorage.getItem(LANGUAGE_KEY) || navigator.language || ''
    } catch {
        // localStorage can throw (privacy mode / disabled storage); the empty
        // default below resolves to English (i18n fallbackLng).
    }
    if (lng.toLowerCase().startsWith('ko')) {
        return {
            title: '오류가 발생했습니다',
            body: '예상치 못한 오류가 발생했습니다. 페이지를 새로고침해 주세요.',
            reload: '새로고침',
        }
    }
    return {
        title: 'Something went wrong',
        body: 'An unexpected error occurred. Please refresh the page.',
        reload: 'Refresh',
    }
}

// isChunkLoadError detects a failed dynamic import — the symptom of an
// already-open tab requesting a lazy chunk whose hash changed across a panel
// upgrade (the old hash now 404s). These are recoverable by reloading.
function isChunkLoadError(error: Error | null): boolean {
    const msg = error?.message || ''
    return /dynamically imported module|module script failed|Failed to fetch dynamically|ChunkLoadError|Loading chunk/i.test(
        msg,
    )
}

export class ErrorBoundary extends Component<Props, State> {
    constructor(props: Props) {
        super(props)
        this.state = { hasError: false, error: null }
    }

    static getDerivedStateFromError(error: Error): State {
        return { hasError: true, error }
    }

    componentDidCatch(error: Error, info: React.ErrorInfo) {
        console.error('ErrorBoundary caught:', error, info.componentStack)
        // Belt-and-suspenders for the post-upgrade stale-chunk case if the
        // vite:preloadError handler in main.tsx didn't catch it: reload once to
        // pull the fresh shell. A 10s timestamp guard prevents a reload loop.
        if (isChunkLoadError(error)) {
            const KEY = 'sfpanel:chunk-reload-at'
            const last = Number(sessionStorage.getItem(KEY) || 0)
            if (Date.now() - last > 10000) {
                sessionStorage.setItem(KEY, String(Date.now()))
                window.location.reload()
            }
        }
    }

    render() {
        if (this.state.hasError) {
            const copy = crashCopy()
            return (
                <div className="flex items-center justify-center min-h-screen bg-background">
                    <div className="text-center p-8 max-w-md">
                        <h1 className="text-2xl font-bold mb-4">{copy.title}</h1>
                        <p className="text-muted-foreground mb-6">{copy.body}</p>
                        <button
                            onClick={() => window.location.reload()}
                            className="px-4 py-2 bg-primary text-primary-foreground rounded-md hover:bg-primary/90 outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                        >
                            {copy.reload}
                        </button>
                    </div>
                </div>
            )
        }
        return this.props.children
    }
}
