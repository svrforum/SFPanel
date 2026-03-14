import type { ReactNode } from 'react'

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
