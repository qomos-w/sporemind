import type { ProjectRef } from '../gen-types/workspace'
import type { ProjectSnapshot } from '../domain/types'

export function mapProjectRef(project: ProjectRef): ProjectSnapshot {
  const rootMount = { Name: project.Name, Path: project.Path, Permission: project.Root ? 'root' : 'rw' }
  const subMounts = (project.Mounts ?? []).map(m => ({
    Name: m.Name,
    Path: m.Path,
    Permission: 'rw' as const,
  }))
  return {
    ProjectID: project.ActorId!,
    Name: project.Name,
    RootPath: project.Path,
    IsOpen: false,
    Mounts: [rootMount, ...subMounts],
    LastOpenedAt: project.LastOpenedAt,
    PermissionMode: project.PermissionMode,
    System: project.System,
    AppKind: project.AppKind,
  }
}

/** Display name for a project. System meta projects always render as the i18n label. */
export function projectDisplayName(
  project: { Name?: string; System?: boolean } | null | undefined,
  t: (key: 'project.systemProject') => string,
): string {
  if (!project) return ''
  if (project.System) return t('project.systemProject')
  return project.Name ?? ''
}
