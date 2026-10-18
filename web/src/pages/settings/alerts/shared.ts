// Constants shared by the alert section components — the rules list and the
// history table render the same type/severity badges.

// Rule type value <-> i18n key mapping. Labels are resolved via t() at render time.
//
// These are the types the rule form offers: the ones the server evaluates.
// e2e/tests/alert-settings.spec.ts holds the form to rule_types in
// internal/feature/alert/testdata/page_contract.json, which the Go tests pin
// to the evaluators. Service, Login and Package were offered too until #132,
// and nothing ever evaluated them; a rule of any type not listed here shows
// by its raw type, and the server's evaluated field says it never alerts.
export const RULE_TYPES: { value: string; i18nKey: string }[] = [
  { value: 'cpu', i18nKey: 'settings.alerts.ruleType.cpu' },
  { value: 'memory', i18nKey: 'settings.alerts.ruleType.memory' },
  { value: 'disk', i18nKey: 'settings.alerts.ruleType.disk' },
  { value: 'container_down', i18nKey: 'settings.alerts.ruleType.containerDown' },
  { value: 'container_oom', i18nKey: 'settings.alerts.ruleType.containerOom' },
  { value: 'container_restart_loop', i18nKey: 'settings.alerts.ruleType.containerRestartLoop' },
  { value: 'container_unhealthy', i18nKey: 'settings.alerts.ruleType.containerUnhealthy' },
]

// Severity labels resolve via t() with the English fallback so the badge
// stays readable even before the locale files carry the keys.
export const SEVERITY_OPTIONS = [
  { value: 'info', i18nKey: 'settings.alerts.severity.info', fallback: 'Info', color: 'bg-primary/10 text-primary' },
  { value: 'warning', i18nKey: 'settings.alerts.severity.warning', fallback: 'Warning', color: 'bg-warning/10 text-warning' },
  { value: 'critical', i18nKey: 'settings.alerts.severity.critical', fallback: 'Critical', color: 'bg-destructive/10 text-destructive' },
]

export function getSeverityStyle(severity: string) {
  return SEVERITY_OPTIONS.find(s => s.value === severity)?.color || 'bg-secondary text-muted-foreground'
}
