import type { I18nKey } from '../../../i18n/types'

const GREETING_KEYS: I18nKey[] = [
  'onboarding.coordinator.home.greeting.1',
  'onboarding.coordinator.home.greeting.2',
  'onboarding.coordinator.home.greeting.3',
  'onboarding.coordinator.home.greeting.4',
  'onboarding.coordinator.home.greeting.5',
  'onboarding.coordinator.home.greeting.6',
]

export function randomGreeting(name: string, t: (key: I18nKey) => string): string {
  const key = GREETING_KEYS[Math.floor(Math.random() * GREETING_KEYS.length)]
  if (!key) return name
  return t(key).replace('{name}', name)
}

const WELCOME_KEYS: I18nKey[] = [
  'shell.newChat.welcome.1',
  'shell.newChat.welcome.2',
  'shell.newChat.welcome.3',
  'shell.newChat.welcome.4',
  'shell.newChat.welcome.5',
]

/** Random build-flavored welcome line for a new agent's empty conversation. */
export function randomWelcome(t: (key: I18nKey) => string): string {
  const key = WELCOME_KEYS[Math.floor(Math.random() * WELCOME_KEYS.length)]
  return key ? t(key) : ''
}

// greetingIssuedThisSession gates the coordinator greeting to once per app
// session. It is a React module-level flag on purpose: sessionStorage is
// forbidden by project constraints and the greeting needs no persistence — a
// refresh (new module instance) may legitimately greet again.
let greetingIssuedThisSession = false

/**
 * One greeting per app session. The first call returns a random greeting and
 * marks the session; every later call returns '' so the coordinator never
 * re-greets on repeated mounts. Callers should fall back to a neutral title
 * when the return value is empty.
 */
export function greetingOnce(name: string, t: (key: I18nKey) => string): string {
  if (greetingIssuedThisSession) return ''
  greetingIssuedThisSession = true
  return randomGreeting(name, t)
}
