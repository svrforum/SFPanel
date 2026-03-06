import { useState, useEffect, useRef } from 'react'
import { useTranslation } from 'react-i18next'
import { Search, Star, ShieldCheck, Loader2 } from 'lucide-react'
import { Input } from '@/components/ui/input'
import { api } from '@/lib/api'
import type { DockerHubSearchResult } from '@/types/api'

interface DockerHubSearchProps {
  value: string
  onChange: (value: string) => void
  placeholder?: string
}

export default function DockerHubSearch({ value, onChange, placeholder }: DockerHubSearchProps) {
  const { t } = useTranslation()
  const [query, setQuery] = useState(value)
  const [results, setResults] = useState<DockerHubSearchResult[]>([])
  const [loading, setLoading] = useState(false)
  const [showDropdown, setShowDropdown] = useState(false)
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    setQuery(value)
  }, [value])

  useEffect(() => {
    const handleClickOutside = (e: MouseEvent) => {
      if (containerRef.current && !containerRef.current.contains(e.target as Node)) {
        setShowDropdown(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  const handleSearch = (term: string) => {
    setQuery(term)
    onChange(term)

    if (debounceRef.current) clearTimeout(debounceRef.current)

    if (term.trim().length < 2) {
      setResults([])
      setShowDropdown(false)
      return
    }

    debounceRef.current = setTimeout(async () => {
      setLoading(true)
      try {
        const data = await api.searchDockerHub(term.trim(), 10)
        setResults(data || [])
        setShowDropdown(true)
      } catch {
        setResults([])
      } finally {
        setLoading(false)
      }
    }, 300)
  }

  const handleSelect = (name: string) => {
    setQuery(name)
    onChange(name)
    setShowDropdown(false)
  }

  return (
    <div className="relative" ref={containerRef}>
      <div className="relative">
        <Search className="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
        <Input
          value={query}
          onChange={(e) => handleSearch(e.target.value)}
          onFocus={() => results.length > 0 && setShowDropdown(true)}
          placeholder={placeholder || t('docker.search.placeholder')}
          className="pl-9 h-9 rounded-xl bg-secondary/50 border-0 text-[13px]"
        />
        {loading && (
          <Loader2 className="absolute right-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground animate-spin" />
        )}
      </div>

      {showDropdown && results.length > 0 && (
        <div className="absolute z-50 w-full mt-1 bg-card rounded-xl card-shadow border border-border/50 max-h-[300px] overflow-y-auto">
          {results.map((r) => (
            <div
              key={r.name}
              className="px-3 py-2 cursor-pointer hover:bg-secondary/50 transition-colors first:rounded-t-xl last:rounded-b-xl"
              onClick={() => handleSelect(r.name)}
            >
              <div className="flex items-center gap-2">
                <span className="text-[13px] font-medium truncate">{r.name}</span>
                {r.is_official && (
                  <span className="inline-flex items-center gap-0.5 px-1.5 py-0 rounded text-[10px] font-medium bg-primary/10 text-primary">
                    <ShieldCheck className="h-3 w-3" />
                    {t('docker.search.official')}
                  </span>
                )}
                <span className="ml-auto flex items-center gap-0.5 text-[11px] text-muted-foreground shrink-0">
                  <Star className="h-3 w-3" />
                  {r.star_count.toLocaleString()}
                </span>
              </div>
              {r.description && (
                <p className="text-[11px] text-muted-foreground mt-0.5 line-clamp-1">{r.description}</p>
              )}
            </div>
          ))}
        </div>
      )}

      {showDropdown && !loading && results.length === 0 && query.trim().length >= 2 && (
        <div className="absolute z-50 w-full mt-1 bg-card rounded-xl card-shadow border border-border/50 p-4 text-center text-[13px] text-muted-foreground">
          {t('docker.search.noResults')}
        </div>
      )}
    </div>
  )
}
