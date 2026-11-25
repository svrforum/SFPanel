import { useState, type ReactElement } from 'react'
import { useTranslation } from 'react-i18next'
import { Drawer } from 'vaul'
import { Popover, PopoverContent, PopoverTrigger } from '@/components/ui/popover'
import { useIsMobile } from '@/hooks/useIsMobile'
import { useServerScope } from '@/hooks/useServerScope'
import { ServerList } from './ServerList'

/**
 * Opens the server list from the element it wraps (its trigger): a popover on
 * a desktop, a sheet from the bottom on a phone. Each open asks for the list
 * again; a pick switches the server (useServerScope's switchTo) and closes.
 */
export function ServerSwitcher({ children, align = 'start' }: { children: ReactElement; align?: 'start' | 'end' }) {
  const { t } = useTranslation()
  const isMobile = useIsMobile()
  const { switchTo, reloadNodes } = useServerScope()
  const [open, setOpen] = useState(false)

  const onOpenChange = (next: boolean) => {
    setOpen(next)
    if (next) reloadNodes()
  }
  const pick = (id: string) => {
    switchTo(id)
    setOpen(false)
  }

  if (isMobile) {
    return (
      <Drawer.Root open={open} onOpenChange={onOpenChange}>
        <Drawer.Trigger asChild>{children}</Drawer.Trigger>
        <Drawer.Portal>
          <Drawer.Overlay className="fixed inset-0 z-50 bg-black/40" />
          <Drawer.Content aria-describedby={undefined} className="fixed bottom-0 left-0 right-0 z-50 flex max-h-[80vh] flex-col rounded-t-2xl bg-card outline-none">
            <div className="mx-auto mt-3 mb-2 h-1.5 w-12 shrink-0 rounded-full bg-muted-foreground/20" />
            <Drawer.Title className="shrink-0 px-4 text-[15px] font-semibold">{t('layout.server.pick')}</Drawer.Title>
            {/* No autofocus: it would raise the phone's keyboard over the list. */}
            <div className="flex min-h-0 flex-1 flex-col px-2 pb-safe">
              <ServerList onPick={pick} />
            </div>
          </Drawer.Content>
        </Drawer.Portal>
      </Drawer.Root>
    )
  }

  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger asChild>{children}</PopoverTrigger>
      <PopoverContent
        align={align}
        aria-label={t('layout.server.pick')}
        className="flex max-h-[min(32rem,var(--radix-popover-content-available-height))] w-72 flex-col p-0"
      >
        <ServerList onPick={pick} autoFocus />
      </PopoverContent>
    </Popover>
  )
}
