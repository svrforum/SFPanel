import { useTranslation } from 'react-i18next'

/**
 * The line under a field: its hint, or — once the dialog's action was pressed
 * with the field empty — that it is required. The action stays pressable: a
 * button greyed out until the form is complete said nothing about what was
 * missing.
 */
export function FieldNote({ id, missing, hint, message }: {
  id: string
  missing: boolean
  hint?: string
  /** Replaces "Required" where the rule is more than "not empty". */
  message?: string
}) {
  const { t } = useTranslation()
  if (!missing && !hint) return null
  return (
    <p id={id} className={`text-[12px] ${missing ? 'text-destructive' : 'text-muted-foreground'}`}>
      {missing ? message ?? t('disk.required') : hint}
    </p>
  )
}

