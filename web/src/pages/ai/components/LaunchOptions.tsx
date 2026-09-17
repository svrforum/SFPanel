import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { AlertTriangle } from 'lucide-react'
import type { AILaunchOptions } from '@/types/api'
import type { LaunchExtraError } from '@/lib/aiSessions'
import { LAUNCH_CATALOGUE, dangerousBlocked, launchSummary } from '@/lib/aiSessions'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'

// The "leave it to the tool" row of each Select. Radix reads an empty item
// value as "cleared", so the absence of a choice needs a sentinel of its own;
// no CLI mode is parenthesised, so it cannot collide with one.
const NONE = '(none)'

/**
 * The dialog's 실행 옵션 section: how the tool starts, beyond the tool itself
 * (spec §6). Collapsed unless the folder is remembered as having been run
 * with options, and then the collapsed line says which — nothing here is
 * ever applied invisibly.
 *
 * The state lives in the dialog, which is what posts it and what remembers
 * it per (node, tool, directory); this component renders it and reports
 * changes. The permission and sandbox values are the CLIs' own words
 * (LAUNCH_CATALOGUE), because an operator checking the panel against
 * `claude --help` needs to find the same name there.
 *
 * `extraError` and `modelError` are computed by the dialog from the same
 * validateExtra / validateModel that block 만들기, so the message beside a
 * field and the refusal that stops the request can never disagree. Both
 * mirror a server rule; catching them here is what keeps the server's English
 * sentence out of a Korean dialog.
 */
export function LaunchOptions({
  tool,
  runAs,
  value,
  onChange,
  extraRaw,
  onExtraRawChange,
  extraError,
  modelError,
  defaultOpen,
}: {
  tool: 'claude' | 'codex'
  runAs: string
  value: AILaunchOptions
  onChange: (o: AILaunchOptions) => void
  extraRaw: string
  onExtraRawChange: (raw: string) => void
  extraError: LaunchExtraError | null
  modelError: boolean
  defaultOpen: boolean
}) {
  const { t } = useTranslation()
  // Until the operator toggles the section it follows defaultOpen, so options
  // remembered for a folder open the section on the render that loads them;
  // after a toggle their choice wins. A prop-syncing effect would be the
  // other way to do this, and setting state in an effect is what this
  // codebase lints as an error.
  const [override, setOverride] = useState<boolean | null>(null)
  const open = override ?? defaultOpen

  const cat = LAUNCH_CATALOGUE[tool]
  const blocked = dangerousBlocked(tool, runAs)
  const summary = launchSummary(tool, value, t)
  const set = (patch: Partial<AILaunchOptions>) => onChange({ ...value, ...patch })

  return (
    <details
      open={open}
      onToggle={(e) => setOverride(e.currentTarget.open)}
      className="rounded-xl border bg-secondary/20 px-3 py-2"
    >
      {/* The native <summary> keeps the disclosure keyboard behaviour and its
          accessible name (its own text) without a Collapsible component. */}
      <summary className="flex cursor-pointer flex-wrap items-center gap-x-2 text-[13px] font-medium">
        {t('ai.launch.section')}
        {!open && summary && (
          <span className="font-normal text-[11px] text-muted-foreground">{summary}</span>
        )}
      </summary>
      <div className="mt-3 space-y-3">
        <div className="space-y-1.5">
          <Label htmlFor="ai-launch-continue">{t('ai.launch.continue')}</Label>
          <Select
            value={value.continue ?? NONE}
            onValueChange={(v) => set({ continue: v === NONE ? undefined : (v as 'last' | 'pick') })}
          >
            <SelectTrigger id="ai-launch-continue" className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>{t('ai.launch.continueNone')}</SelectItem>
              <SelectItem value="last">{t('ai.launch.continueLast')}</SelectItem>
              <SelectItem value="pick">{t('ai.launch.continuePick')}</SelectItem>
            </SelectContent>
          </Select>
          {/* The hint names the behaviour, not the flag: Claude continues the
              most recent conversation *in this directory*, Codex its most
              recent session. */}
          <p className="text-[11px] text-muted-foreground">{t(`ai.launch.continueHint.${tool}`)}</p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="ai-launch-permission">{t('ai.launch.permission')}</Label>
          <Select
            value={value.permission || NONE}
            onValueChange={(v) => set({ permission: v === NONE ? undefined : v })}
          >
            <SelectTrigger id="ai-launch-permission" className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value={NONE}>{t('ai.launch.default')}</SelectItem>
              {cat.permissions.map((p) => (
                <SelectItem key={p} value={p} className="font-mono text-[12px]">{p}</SelectItem>
              ))}
            </SelectContent>
          </Select>
          <p className="text-[11px] text-muted-foreground">{t(`ai.launch.permissionHint.${tool}`)}</p>
        </div>
        {/* Codex only. Claude has no sandbox flag, and offering one would be a
            control whose value the server refuses. */}
        {cat.sandboxes && (
          <div className="space-y-1.5">
            <Label htmlFor="ai-launch-sandbox">{t('ai.launch.sandbox')}</Label>
            <Select
              value={value.sandbox || NONE}
              onValueChange={(v) => set({ sandbox: v === NONE ? undefined : v })}
            >
              <SelectTrigger id="ai-launch-sandbox" className="w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value={NONE}>{t('ai.launch.default')}</SelectItem>
                {cat.sandboxes.map((sb) => (
                  <SelectItem key={sb} value={sb} className="font-mono text-[12px]">{sb}</SelectItem>
                ))}
              </SelectContent>
            </Select>
            <p className="text-[11px] text-muted-foreground">{t('ai.launch.sandboxHint')}</p>
          </div>
        )}
        <div className="space-y-1.5">
          <Label htmlFor="ai-launch-model">{t('ai.launch.model')}</Label>
          {/* Trimmed as it is typed: the field is one CLI token, so
              surrounding whitespace is never part of a model name — and a
              name pasted out of a document arrives with it. What the trim
              cannot rescue (a `/` or an `@`, which no model name either CLI
              takes may carry) is named below instead of being posted for the
              server to refuse in English. */}
          <Input
            id="ai-launch-model"
            value={value.model ?? ''}
            onChange={(e) => set({ model: e.target.value.trim() || undefined })}
            placeholder={t('ai.launch.modelPlaceholder')}
            className="font-mono text-[12px]"
            maxLength={64}
            spellCheck={false}
            aria-invalid={modelError}
          />
          {modelError && (
            <p role="alert" className="text-[11px] text-destructive">{t('ai.launch.modelError')}</p>
          )}
        </div>
        <div className="space-y-1.5">
          <label className={`flex items-center gap-2 text-[13px] ${blocked ? 'text-muted-foreground' : 'cursor-pointer'}`}>
            <Checkbox
              checked={value.dangerous === true && !blocked}
              disabled={blocked}
              onCheckedChange={(v) => set({ dangerous: v === true || undefined })}
            />
            {t(`ai.launch.dangerous.${tool}`)}
          </label>
          {/* Disabled for a root Claude: the CLI refuses the flag itself
              ("cannot be used with root/sudo privileges"), so the reason is
              said here, beside the account selector that fixes it, instead of
              letting the session die with it. */}
          <p className={`flex items-start gap-1 text-[11px] ${blocked ? 'text-muted-foreground' : 'text-warning'}`}>
            <AlertTriangle className="mt-px h-3 w-3 shrink-0" aria-hidden="true" />
            {blocked ? t('ai.launch.dangerousRoot') : t('ai.launch.dangerousWarning')}
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="ai-launch-extra">{t('ai.launch.extra')}</Label>
          <Input
            id="ai-launch-extra"
            value={extraRaw}
            onChange={(e) => onExtraRawChange(e.target.value)}
            placeholder={t('ai.launch.extraPlaceholder')}
            className="font-mono text-[12px]"
            spellCheck={false}
            aria-invalid={extraError !== null}
            aria-describedby="ai-launch-extra-hint"
          />
          {/* The rule, then the refusal that names which part of it was
              broken — before anything is posted. */}
          <p id="ai-launch-extra-hint" className="text-[11px] text-muted-foreground">{t('ai.launch.extraHint')}</p>
          {extraError && (
            <p role="alert" className="text-[11px] text-destructive">{t(`ai.launch.extraErrors.${extraError}`)}</p>
          )}
        </div>
      </div>
    </details>
  )
}
