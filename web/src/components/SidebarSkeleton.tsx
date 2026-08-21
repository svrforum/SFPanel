/**
 * Placeholder that holds the sidebar's slot while the cluster probe is still
 * in flight.
 *
 * Rendering nothing while loading (what Layout and ClusterSidebar both used to
 * do) reads as the sidebar vanishing: on a cluster whose leader is unreachable
 * /cluster/status can take seconds, and the shell reflowed to full width and
 * back. Keeping the slot occupied makes the wait look like loading instead of
 * a glitch.
 */
export default function SidebarSkeleton({ widthPx = 240 }: { widthPx?: number }) {
  return (
    <div
      className="hidden md:flex flex-col h-full shrink-0 bg-card border-r border-border"
      style={{ width: widthPx }}
      aria-hidden="true"
    >
      <div className="px-4 py-4">
        <div className="h-8 w-full rounded-lg bg-secondary animate-pulse" />
      </div>
      <div className="flex-1 px-3 space-y-1.5">
        {Array.from({ length: 8 }, (_, i) => (
          <div key={i} className="h-8 rounded-xl bg-secondary animate-pulse" />
        ))}
      </div>
    </div>
  )
}
