// Shared App Store helpers.

const ICON_BASE = 'https://raw.githubusercontent.com/svrforum/SFPanel-appstore/main/apps'

/**
 * appStoreIconUrl returns the icon URL for an app, preferring an explicit
 * override (the `icon` field from the catalog metadata) and falling back to
 * the conventional per-app icon path in the SFPanel-appstore repo.
 */
export function appStoreIconUrl(id: string, iconOverride?: string): string {
  return iconOverride || `${ICON_BASE}/${id}/icon.svg`
}
