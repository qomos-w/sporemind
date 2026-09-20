import { useEffect, useState } from 'react'
import * as slashCommandsApi from '../../../gen-clients/workspace/client'
import * as builtinModesApi from '../../../gen-clients/workspace/client'
import { client } from '../../../application/generated-client'
import { listSkills, resolveSkillOverrides } from '../../../application/skill-card-store'
import type { SlashCommand } from '../../../gen-types/workspace.slashcommand'
import type { BuiltinMode } from '../../../gen-types/workspace.builtin.modes'

export interface UseSlashCommandsResult {
  commands: SlashCommand[]
  skills: SlashCommand[]
  modes: BuiltinMode[]
  loading: boolean
  error: string | null
}

type SlashItems = Pick<UseSlashCommandsResult, 'commands' | 'skills' | 'modes'>

// Slash commands and builtin modes are static for the process lifetime; cache
// the first fetch so repeated composer mounts do not re-hit the backend. The
// modes list powers the composer's slash-mode interception filter ("/<name>"
// mounts the matching builtin mode without submitting).
const cachedByProject = new Map<string, SlashItems>()
const inflightByProject = new Map<string, Promise<SlashItems>>()

export function useSlashCommands(enabled = true, projectId?: string | null): UseSlashCommandsResult {
  const cacheKey = projectId ?? ''
  const cached = cachedByProject.get(cacheKey)
  const [commands, setCommands] = useState<SlashCommand[]>(cached?.commands ?? [])
  const [skills, setSkills] = useState<SlashCommand[]>(cached?.skills ?? [])
  const [modes, setModes] = useState<BuiltinMode[]>(cached?.modes ?? [])
  const [loading, setLoading] = useState(cached === undefined && enabled)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (!enabled) return
    if (cached) {
      setCommands(cached.commands)
      setSkills(cached.skills)
      setModes(cached.modes)
      setLoading(false)
      return
    }
    let active = true
    let inflight = inflightByProject.get(cacheKey)
    if (!inflight) {
      inflight = Promise.all([
        slashCommandsApi.slashCommandsList(client),
        listSkills(client, projectId),
        builtinModesApi.builtinModesList(client),
      ]).then(([resp, skills, modesResp]) => {
        const commands = resp.Commands ?? []
        const commandNames = new Set(commands.map((command) => command.Name))
        const items = {
          commands,
          // Resolve same-name collisions by priority (project internal >
          // system internal > project external) so an external skill only
          // surfaces when no higher-priority skill shares its name. Skills
          // whose id clashes with a real slash command are dropped below.
          skills: resolveSkillOverrides(skills)
            .filter((skill) => !commandNames.has(skill.Id))
            .map((skill) => ({ Name: skill.Id, ShortHelp: skill.Description || skill.Name })),
          modes: modesResp.Modes ?? [],
        }
        cachedByProject.set(cacheKey, items)
        inflightByProject.delete(cacheKey)
        return items
      })
      inflightByProject.set(cacheKey, inflight)
    }
    setLoading(true)
    inflight
      .then((items) => {
        if (active) {
          setCommands(items.commands)
          setSkills(items.skills)
          setModes(items.modes)
          setLoading(false)
        }
      })
      .catch((err: unknown) => {
        inflightByProject.delete(cacheKey)
        if (active) {
          setError(err instanceof Error ? err.message : String(err))
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [enabled, projectId, cacheKey])

  return { commands, skills, modes, loading, error }
}
