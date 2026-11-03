import { useRef, useState, type ChangeEvent } from 'react'
import { useTranslation } from 'react-i18next'
import { Eye, EyeOff, FileUp } from 'lucide-react'
import { cn } from '@/lib/utils'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import type { AgentInstallLogin, AgentInstallMethod } from '@/types/api'
import { EditorCheck, EditorField } from '../TaskStepCard'
import { methodText, type InstallAccountFields } from './installForm'

const TEXTAREA = cn(
  'w-full min-h-24 rounded-md border border-input bg-transparent px-3 py-2 font-mono text-[13px] shadow-xs outline-none',
  'focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/50 disabled:opacity-50 dark:bg-input/30',
)
// No private key comes near this; a file this large is some other file.
const MAX_KEY_FILE_BYTES = 64 * 1024
const METHODS: AgentInstallMethod[] = ['password', 'key', 'tailscale']

interface AccountFieldsProps {
  /** The prefix of the fields' IDs. */
  id: string
  value: InstallAccountFields
  onChange: (patch: Partial<InstallAccountFields>) => void
  /** The problem to show under a field, by the request's name for it. */
  problem: (field: string) => string | undefined
  /**
   * A saved login being changed: which secrets it stores. A field left
   * empty keeps the stored secret where the method uses it, and its hint
   * says so; nothing is said of a secret that is not stored.
   */
  stored?: Pick<AgentInstallLogin, 'has_password' | 'has_key' | 'has_passphrase' | 'has_sudo_password'>
  disabled?: boolean
}

// The SSH account: the user, the login method, that method's secrets, and
// the sudo password. The install form's, and a saved login's change. The
// secrets are the caller's state: these fields keep none of their own.
export function AccountFields({ id, value, onChange, problem, stored, disabled }: AccountFieldsProps) {
  const { t } = useTranslation()
  const rootUser = value.user.trim() === 'root'
  return (
    <>
      <div className="max-w-xs">
        <EditorField id={`${id}-user`} label={t('cluster.tasks.install.setup.user')} error={problem('user')}>
          <Input
            id={`${id}-user`}
            value={value.user}
            onChange={(e) => onChange({ user: e.target.value })}
            placeholder="ubuntu"
            autoComplete="off"
            autoCapitalize="none"
            spellCheck={false}
            aria-invalid={!!problem('user') || undefined}
            disabled={disabled}
          />
        </EditorField>
      </div>
      <div className="space-y-1">
        <p id={`${id}-method`} className="text-[12px] font-medium text-muted-foreground">{t('cluster.tasks.install.setup.method')}</p>
        <div role="radiogroup" aria-labelledby={`${id}-method`} className="flex flex-wrap gap-x-4 gap-y-1">
          {METHODS.map((m) => (
            <label key={m} className="flex items-center gap-2 min-h-9 text-[13px] cursor-pointer">
              <input
                type="radio"
                name={`${id}-method`}
                checked={value.method === m}
                disabled={disabled}
                onChange={() => onChange({ method: m })}
                className="accent-primary"
              />
              {methodText(m, t)}
            </label>
          ))}
        </div>
        {problem('method') && <p className="text-[12px] text-destructive">{problem('method')}</p>}
      </div>

      {value.method === 'password' && (
        <div className="max-w-xs">
          <SecretField
            id={`${id}-password`}
            label={t('cluster.tasks.install.setup.password')}
            showLabel={t('cluster.tasks.install.setup.showPassword')}
            hideLabel={t('cluster.tasks.install.setup.hidePassword')}
            value={value.password}
            onChange={(password) => onChange({ password })}
            hint={stored?.has_password ? t('cluster.tasks.install.logins.keepPassword') : undefined}
            error={problem('password')}
            disabled={disabled}
          />
        </div>
      )}
      {value.method === 'key' && (
        <KeyFields
          id={id}
          keyText={value.key}
          passphrase={value.passphrase}
          onKey={(key) => onChange({ key })}
          onPassphrase={(passphrase) => onChange({ passphrase })}
          keyHint={stored?.has_key ? t('cluster.tasks.install.logins.keepKey') : undefined}
          // The stored passphrase stays with the stored key; a new key
          // takes the one typed here, or none.
          passphraseHint={
            stored?.has_passphrase && !value.key.trim()
              ? t('cluster.tasks.install.logins.keepPassphrase')
              : t('cluster.tasks.install.setup.passphraseHint')
          }
          keyError={problem('key')}
          passphraseError={problem('passphrase')}
          disabled={disabled}
        />
      )}
      {value.method === 'tailscale' && (
        <p className="text-[12px] text-muted-foreground max-w-prose">{t('cluster.tasks.install.setup.tailscaleHint')}</p>
      )}

      {rootUser ? (
        <p className="text-[12px] text-muted-foreground">{t('cluster.tasks.install.setup.sudoRoot')}</p>
      ) : (
        <div className="space-y-2">
          {value.method === 'password' && (
            <EditorCheck
              id={`${id}-sudo-same`}
              label={t('cluster.tasks.install.setup.sudoSame')}
              checked={value.sudoSame}
              onChange={(sudoSame) => onChange({ sudoSame })}
              disabled={disabled}
            />
          )}
          {problem('sudo_same') && <p className="text-[12px] text-destructive">{problem('sudo_same')}</p>}
          {(value.method !== 'password' || !value.sudoSame) && (
            <div className="max-w-xs">
              <SecretField
                id={`${id}-sudo`}
                label={t('cluster.tasks.install.setup.sudoPassword')}
                showLabel={t('cluster.tasks.install.setup.showSudo')}
                hideLabel={t('cluster.tasks.install.setup.hideSudo')}
                value={value.sudoPassword}
                onChange={(sudoPassword) => onChange({ sudoPassword })}
                // Left empty, a stored one stays: it is not taken away here.
                hint={stored?.has_sudo_password ? t('cluster.tasks.install.logins.keepSudo') : t('cluster.tasks.install.setup.sudoHint')}
                error={problem('sudo_password')}
                disabled={disabled}
              />
            </div>
          )}
        </div>
      )}
    </>
  )
}

// A secret typed in: masked unless the operator asks to see it, and kept out
// of the browser's password manager.
function SecretField({ id, label, showLabel, hideLabel, value, onChange, hint, error, disabled }: {
  id: string
  label: string
  showLabel: string
  hideLabel: string
  value: string
  onChange: (value: string) => void
  hint?: string
  error?: string
  disabled?: boolean
}) {
  const [visible, setVisible] = useState(false)
  return (
    <EditorField id={id} label={label} hint={hint} error={error}>
      <div className="relative">
        <Input
          id={id}
          type={visible ? 'text' : 'password'}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          autoComplete="off"
          autoCapitalize="none"
          spellCheck={false}
          aria-invalid={!!error || undefined}
          disabled={disabled}
          className="pr-10"
        />
        <button
          type="button"
          onClick={() => setVisible((v) => !v)}
          className="absolute right-2 top-1/2 -translate-y-1/2 text-muted-foreground rounded-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
          aria-label={visible ? hideLabel : showLabel}
        >
          {visible ? <EyeOff className="h-4 w-4" /> : <Eye className="h-4 w-4" />}
        </button>
      </div>
    </EditorField>
  )
}

// The private key, pasted or read from a file, and its passphrase. A text
// area cannot be masked; it is kept from spelling, correction and autofill.
function KeyFields({ id, keyText, passphrase, onKey, onPassphrase, keyHint, passphraseHint, keyError, passphraseError, disabled }: {
  id: string
  keyText: string
  passphrase: string
  onKey: (key: string) => void
  onPassphrase: (passphrase: string) => void
  keyHint?: string
  passphraseHint: string
  keyError?: string
  passphraseError?: string
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const file = useRef<HTMLInputElement>(null)
  const [fileError, setFileError] = useState<string | null>(null)

  const readFile = (e: ChangeEvent<HTMLInputElement>) => {
    const chosen = e.target.files?.[0]
    // Cleared, so the same file can be chosen again.
    e.target.value = ''
    if (!chosen) return
    if (chosen.size > MAX_KEY_FILE_BYTES) {
      setFileError(t('cluster.tasks.install.setup.keyFileTooLarge'))
      return
    }
    const reader = new FileReader()
    reader.onload = () => {
      setFileError(null)
      onKey(typeof reader.result === 'string' ? reader.result : '')
    }
    reader.onerror = () => setFileError(t('cluster.tasks.install.setup.keyFileError'))
    reader.readAsText(chosen)
  }

  return (
    <div className="space-y-3">
      <EditorField id={`${id}-key`} label={t('cluster.tasks.install.setup.key')} hint={keyHint} error={keyError ?? fileError ?? undefined}>
        <textarea
          id={`${id}-key`}
          value={keyText}
          onChange={(e) => onKey(e.target.value)}
          rows={5}
          placeholder="-----BEGIN OPENSSH PRIVATE KEY-----"
          spellCheck={false}
          autoComplete="off"
          autoCorrect="off"
          autoCapitalize="none"
          aria-invalid={!!keyError || undefined}
          disabled={disabled}
          className={TEXTAREA}
        />
      </EditorField>
      <div>
        <input ref={file} type="file" className="hidden" onChange={readFile} tabIndex={-1} aria-hidden />
        <Button type="button" variant="outline" size="sm" onClick={() => file.current?.click()} disabled={disabled}>
          <FileUp className="h-4 w-4" />
          {t('cluster.tasks.install.setup.keyFile')}
        </Button>
      </div>
      <div className="max-w-xs">
        <SecretField
          id={`${id}-passphrase`}
          label={t('cluster.tasks.install.setup.passphrase')}
          showLabel={t('cluster.tasks.install.setup.showPassphrase')}
          hideLabel={t('cluster.tasks.install.setup.hidePassphrase')}
          value={passphrase}
          onChange={onPassphrase}
          hint={passphraseHint}
          error={passphraseError}
          disabled={disabled}
        />
      </div>
    </div>
  )
}
