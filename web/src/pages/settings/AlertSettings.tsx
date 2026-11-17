import { useState, useEffect, useCallback } from 'react'
import { api } from '@/lib/api'
import { ChannelsSection } from './alerts/ChannelsSection'
import { RulesSection } from './alerts/RulesSection'
import { HistorySection } from './alerts/HistorySection'
import type { AlertChannel } from '@/types/api'

// The three sections (channels / rules / history) are independent CRUD units
// living in pages/settings/alerts/. The channel list is the only shared state
// — rules reference channels by id — so it is owned here and passed down.
//
// With whether it was read at all: a failed read showed "No channels
// configured", and an operator set up the same channel again.
export default function AlertSettings() {
  const [channels, setChannels] = useState<AlertChannel[]>([])
  const [loaded, setLoaded] = useState(false)
  const [loadError, setLoadError] = useState<string | null>(null)

  const loadChannels = useCallback(() => {
    api.getAlertChannels()
      .then((list) => {
        setChannels(list)
        setLoaded(true)
        setLoadError(null)
      })
      .catch((err: unknown) => setLoadError(err instanceof Error ? err.message : String(err)))
  }, [])

  const retryChannels = useCallback(() => {
    setLoadError(null)
    loadChannels()
  }, [loadChannels])

  useEffect(() => {
    loadChannels()
  }, [loadChannels])

  return (
    <div className="space-y-6">
      <ChannelsSection channels={channels} loaded={loaded} loadError={loadError} onRetry={retryChannels} onChanged={loadChannels} />
      <RulesSection channels={channels} channelsFailed={!loaded && !!loadError} />
      <HistorySection />
    </div>
  )
}
