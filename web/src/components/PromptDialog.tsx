import { createContext, useCallback, useContext, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
} from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

export interface PromptOptions {
  title: string
  description?: string
  placeholder?: string
  defaultValue?: string
  /** Render the input as a password field (masked). */
  password?: boolean
  confirmLabel?: string
  cancelLabel?: string
}

type PromptFn = (opts: PromptOptions) => Promise<string | null>

const PromptContext = createContext<PromptFn | null>(null)

/**
 * usePrompt returns an async prompt(opts) that resolves to the entered string,
 * or null if cancelled — a styled, on-brand replacement for window.prompt.
 *   const v = await prompt({ title: t('...'), password: true }); if (!v) return
 *
 * Co-located with its provider (one module owns the prompt machinery); the only
 * cost is HMR fast-refresh for this file, acknowledged by the disable below.
 */
// eslint-disable-next-line react-refresh/only-export-components
export function usePrompt(): PromptFn {
  const ctx = useContext(PromptContext)
  if (!ctx) throw new Error('usePrompt must be used within <PromptProvider>')
  return ctx
}

interface PendingState {
  opts: PromptOptions
  resolve: (result: string | null) => void
}

export function PromptProvider({ children }: { children: ReactNode }) {
  const { t } = useTranslation()
  const [pending, setPending] = useState<PendingState | null>(null)
  const [value, setValue] = useState('')

  const prompt = useCallback<PromptFn>((opts) => {
    setValue(opts.defaultValue ?? '')
    return new Promise<string | null>((resolve) => {
      setPending({ opts, resolve })
    })
  }, [])

  const settle = useCallback((result: string | null) => {
    setPending((cur) => {
      cur?.resolve(result)
      return null
    })
  }, [])

  const opts = pending?.opts

  return (
    <PromptContext.Provider value={prompt}>
      {children}
      <Dialog open={!!pending} onOpenChange={(open) => { if (!open) settle(null) }}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>{opts?.title}</DialogTitle>
            {opts?.description && <DialogDescription>{opts.description}</DialogDescription>}
          </DialogHeader>
          <form
            onSubmit={(e) => { e.preventDefault(); settle(value) }}
            className="space-y-3"
          >
            <Input
              type={opts?.password ? 'password' : 'text'}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder={opts?.placeholder}
              className="rounded-xl"
              autoFocus
              autoComplete={opts?.password ? 'current-password' : 'off'}
            />
            <DialogFooter>
              <Button type="button" variant="outline" className="rounded-xl" onClick={() => settle(null)}>
                {opts?.cancelLabel ?? t('common.cancel')}
              </Button>
              <Button type="submit" className="rounded-xl">
                {opts?.confirmLabel ?? t('common.confirm')}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </PromptContext.Provider>
  )
}
