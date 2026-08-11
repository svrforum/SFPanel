import { useState, useEffect, useCallback } from 'react'
import { api } from '@/lib/api'
import { ChannelsSection } from './alerts/ChannelsSection'
import { RulesSection } from './alerts/RulesSection'
import { HistorySection } from './alerts/HistorySection'
import type { AlertChannel } from '@/types/api'

// The three sections (channels / rules / history) are independent CRUD units
// living in pages/settings/alerts/. The channel list is the only shared state
// — rules reference channels by id — so it is owned here and passed down.
export default function AlertSettings() {
  const [channels, setChannels] = useState<AlertChannel[]>([])

  const loadChannels = useCallback(() => {
    api.getAlertChannels()
      .then(setChannels)
      .catch(() => { /* ignore */ })
  }, [])

  useEffect(() => {
    loadChannels()
  }, [loadChannels])

  return (
    <div className="space-y-6">
      <ChannelsSection channels={channels} onChanged={loadChannels} />
      <RulesSection channels={channels} />
      <HistorySection />
    </div>
  )
}
