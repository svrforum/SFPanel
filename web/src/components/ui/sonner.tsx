"use client"

import { useEffect, useState } from "react"
import {
  CircleCheckIcon,
  InfoIcon,
  Loader2Icon,
  OctagonXIcon,
  TriangleAlertIcon,
} from "lucide-react"
import { Toaster as Sonner, type ToasterProps } from "sonner"

import { getThemePref, resolveTheme } from "@/lib/theme"

const Toaster = ({ ...props }: ToasterProps) => {
  // Drive the toaster from the app's resolved theme (not the OS directly) so it
  // matches an explicit Light/Dark override, and re-render on theme change.
  const [theme, setTheme] = useState<"light" | "dark">(() => resolveTheme(getThemePref()))
  useEffect(() => {
    const sync = () => setTheme(resolveTheme(getThemePref()))
    window.addEventListener("sfpanel:themechange", sync)
    const mql = window.matchMedia("(prefers-color-scheme: dark)")
    mql.addEventListener("change", sync)
    return () => {
      window.removeEventListener("sfpanel:themechange", sync)
      mql.removeEventListener("change", sync)
    }
  }, [])

  return (
    <Sonner
      theme={theme}
      className="toaster group"
      // Clear the mobile BottomNav (h-14 = 56px) + the iOS home-indicator so
      // success/error toasts don't land on top of the nav bar.
      mobileOffset={{ bottom: "calc(56px + env(safe-area-inset-bottom, 0px) + 12px)" }}
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
        } as React.CSSProperties
      }
      {...props}
    />
  )
}

export { Toaster }
