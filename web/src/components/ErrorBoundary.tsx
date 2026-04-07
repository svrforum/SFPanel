import { Component, type ReactNode } from 'react'
import React from 'react'

interface Props {
    children: ReactNode
}

interface State {
    hasError: boolean
    error: Error | null
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
    }

    render() {
        if (this.state.hasError) {
            return (
                <div className="flex items-center justify-center min-h-screen bg-background">
                    <div className="text-center p-8 max-w-md">
                        <h1 className="text-2xl font-bold mb-4">오류가 발생했습니다</h1>
                        <p className="text-muted-foreground mb-6">
                            예상치 못한 오류가 발생했습니다. 페이지를 새로고침해 주세요.
                        </p>
                        <button
                            onClick={() => window.location.reload()}
                            className="px-4 py-2 bg-primary text-primary-foreground rounded-md hover:bg-primary/90"
                        >
                            새로고침
                        </button>
                    </div>
                </div>
            )
        }
        return this.props.children
    }
}
