import type { TFunction } from 'i18next'

// The built-in tasks carry Korean names, descriptions, input labels and step
// names in the binary (internal/tasks/builtin/*.yaml). The screen finds its
// own words for them by ID. Every key is written out whole so locales.test,
// which only sees keys written out in a call, checks it in both languages.

const KEYS: Record<string, string> = {
  'builtin/package-update': 'packageUpdate',
  'builtin/package-install': 'packageInstall',
  'builtin/service-restart': 'serviceRestart',
  'builtin/command': 'command',
  'builtin/reboot': 'reboot',
}

const TEXT: Record<string, (t: TFunction) => string> = {
  'packageUpdate.name': (t) => t('cluster.tasks.builtins.packageUpdate.name'),
  'packageUpdate.description': (t) => t('cluster.tasks.builtins.packageUpdate.description'),
  'packageUpdate.steps.0': (t) => t('cluster.tasks.builtins.packageUpdate.steps.0'),

  'packageInstall.name': (t) => t('cluster.tasks.builtins.packageInstall.name'),
  'packageInstall.description': (t) => t('cluster.tasks.builtins.packageInstall.description'),
  'packageInstall.inputs.packages': (t) => t('cluster.tasks.builtins.packageInstall.inputs.packages'),
  'packageInstall.steps.0': (t) => t('cluster.tasks.builtins.packageInstall.steps.0'),

  'serviceRestart.name': (t) => t('cluster.tasks.builtins.serviceRestart.name'),
  'serviceRestart.description': (t) => t('cluster.tasks.builtins.serviceRestart.description'),
  'serviceRestart.inputs.service': (t) => t('cluster.tasks.builtins.serviceRestart.inputs.service'),
  'serviceRestart.steps.0': (t) => t('cluster.tasks.builtins.serviceRestart.steps.0'),

  'command.name': (t) => t('cluster.tasks.builtins.command.name'),
  'command.description': (t) => t('cluster.tasks.builtins.command.description'),
  'command.inputs.command': (t) => t('cluster.tasks.builtins.command.inputs.command'),
  'command.steps.0': (t) => t('cluster.tasks.builtins.command.steps.0'),

  'reboot.name': (t) => t('cluster.tasks.builtins.reboot.name'),
  'reboot.description': (t) => t('cluster.tasks.builtins.reboot.description'),
  'reboot.steps.0': (t) => t('cluster.tasks.builtins.reboot.steps.0'),
}

/** The i18n name of a built-in task (builtin/package-update → packageUpdate), or null for any other ID. */
export function builtinKey(id: string): string | null {
  return Object.hasOwn(KEYS, id) ? KEYS[id] : null
}

/**
 * A built-in task's words in the screen's language: field is name,
 * description, inputs.<input> or steps.<index from 0>. Anything else, a
 * stored task or a built-in this screen does not know, keeps the fallback
 * (what the server sent).
 */
export function builtinText(t: TFunction, id: string, field: string, fallback: string): string {
  const key = builtinKey(id)
  if (key === null) return fallback
  const text = Object.hasOwn(TEXT, `${key}.${field}`) ? TEXT[`${key}.${field}`] : undefined
  return text ? text(t) : fallback
}
