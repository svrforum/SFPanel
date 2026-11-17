import { cva } from "class-variance-authority"

// See badge-variants.ts / button-variants.ts for the rationale — keeps
// react-refresh/only-export-components happy without disabling the rule.
//
// justify-center-safe: a list made to scroll on a phone and centred plainly
// pushed its first tab past the left edge, where no scroll reaches.
export const tabsListVariants = cva(
  "rounded-lg p-[3px] group-data-[orientation=horizontal]/tabs:h-9 data-[variant=line]:rounded-none group/tabs-list text-muted-foreground inline-flex w-fit items-center justify-center-safe group-data-[orientation=vertical]/tabs:h-fit group-data-[orientation=vertical]/tabs:flex-col",
  {
    variants: {
      variant: {
        default: "bg-muted",
        line: "gap-1 bg-transparent",
      },
    },
    defaultVariants: {
      variant: "default",
    },
  }
)
