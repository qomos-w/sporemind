export function avatarHue(seed: string): number {
  let hash = 0
  for (let i = 0; i < seed.length; i += 1) {
    hash = ((hash << 5) - hash) + seed.charCodeAt(i)
  }
  return Math.abs(hash) % 360
}

export function avatarLabel(title: string, fallback = 'AG'): string {
  const compact = title.trim().replace(/\s+/g, ' ')
  if (!compact) return fallback
  const words = compact.split(' ').filter(Boolean)
  if (words.length >= 2) {
    return `${words[0]?.[0] ?? fallback[0]}${words[1]?.[0] ?? ''}`.toUpperCase()
  }
  const w = words[0] ?? compact
  const caps = w.replace(/[^A-Z]/g, '')
  if (caps.length >= 2) return caps.slice(0, 2).toUpperCase()
  if (caps.length === 1) return (caps + w.charAt(1)).toUpperCase()
  return w.slice(0, 2).toUpperCase()
}

export function agentAvatarLabel(displayName: string | undefined, agentKind: string | undefined, fallback = 'AG'): string {
  return avatarLabel(displayName || agentKind || fallback, fallback)
}

export function agentDisplayName(title: string | undefined, displayName: string, fallback = 'Agent'): string {
  return title || displayName || fallback
}

// Sidebar/tree agent row label. System-managed agents (workers, reviewers) get
// a random DisplayName at spawn time; their Title is the dynamic task-card goal
// text, so show the Title first, falling back to the DisplayName.
export function sidebarAgentLabel(item: { Title?: string; DisplayName: string; AgentKind: string }): string {
  const kind = (item.AgentKind ?? '').toLowerCase()
  if (kind === 'worker' || kind === 'reviewer') {
    return item.Title || item.DisplayName
  }
  if (kind === 'coordinator') {
    return item.DisplayName
  }
  return agentDisplayName(item.Title, item.DisplayName)
}
