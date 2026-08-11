import { useState, type ReactNode } from 'react'
import { Info, ChevronDown, ChevronUp } from 'lucide-react'

export interface GuideStep {
  num: string
  title: string
  desc: string
}

export interface GuideFact {
  label: string
  value: string
}

// Collapsible "how it works" card shared by CronJobs and Logs (AppStore uses
// the same layout and can adopt it). All strings arrive pre-translated.
export function GuideAccordion({
  title,
  steps,
  facts,
  children,
}: {
  title: string
  steps: GuideStep[]
  /** Bottom row of "label value" facts. */
  facts?: GuideFact[]
  /** Optional extra content rendered between the steps grid and the facts row. */
  children?: ReactNode
}) {
  const [open, setOpen] = useState(false)
  return (
    <div className="bg-card rounded-2xl card-shadow overflow-hidden">
      <button
        onClick={() => setOpen(!open)}
        className="w-full flex items-center gap-2.5 px-4 py-3 text-left hover:bg-secondary/30 transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/40 focus-visible:ring-offset-0"
      >
        <Info className="h-4 w-4 text-primary shrink-0" />
        <span className="text-[13px] font-medium flex-1">{title}</span>
        {open ? (
          <ChevronUp className="h-4 w-4 text-muted-foreground" />
        ) : (
          <ChevronDown className="h-4 w-4 text-muted-foreground" />
        )}
      </button>
      {open && (
        <div className="px-4 pb-4 space-y-3 animate-in slide-in-from-top-1 duration-200">
          <div className="h-px bg-border" />
          <div className="grid grid-cols-1 sm:grid-cols-3 gap-3">
            {steps.map((step) => (
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
          {children}
          {facts && facts.length > 0 && (
            <div className="flex flex-wrap gap-x-4 gap-y-1 pt-1">
              {facts.map((fact) => (
                <span key={fact.label} className="text-[11px] text-muted-foreground">
                  <span className="font-medium text-foreground">{fact.label}</span> {fact.value}
                </span>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}
