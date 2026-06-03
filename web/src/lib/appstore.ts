// Shared App Store helpers.

// Catalog lives in the main repo under /appstore (migrated from the separate
// SFPanel-appstore repo); only ~4 apps ship a local icon.svg here, the rest use
// an absolute `icon` override from their metadata.
const ICON_BASE = 'https://raw.githubusercontent.com/svrforum/SFPanel/main/appstore/apps'

/**
 * appStoreIconUrl returns the icon URL for an app, preferring an explicit
 * override (the `icon` field from the catalog metadata) and falling back to
 * the conventional per-app icon path in the main repo's /appstore.
 */
export function appStoreIconUrl(id: string, iconOverride?: string): string {
  return iconOverride || `${ICON_BASE}/${id}/icon.svg`
}
