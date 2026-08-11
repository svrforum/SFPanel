import type { SelectHTMLAttributes } from 'react'
import { cn } from '@/lib/utils'

/**
 * Native <select> styled to match the panel inputs — single source for the
 * style string previously copy-pasted across the disk dialogs and toolbars.
 */
export function NativeSelect({ className, ...props }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <select
      className={cn(
        'flex h-9 rounded-xl border-0 bg-secondary/50 px-3 py-1 text-[13px] transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-primary/20',
        className,
      )}
      {...props}
    />
  )
}
