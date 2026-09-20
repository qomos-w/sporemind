import { describe, it, expect } from 'vitest'
import { inferToolNameFromInput } from './tool-parsers.ts'

describe('inferToolNameFromInput', () => {
  describe('canonical dotted IDs pass through', () => {
    const cases: Array<[string, string]> = [
      ['project.read', 'project.read'],
      ['project.read_base64', 'project.read_base64'],
      ['project.read_chunk', 'project.read_chunk'],
      ['project.write', 'project.write'],
      ['project.edit', 'project.edit'],
      ['project.glob', 'project.glob'],
      ['project.grep', 'project.grep'],
      ['project.list', 'project.list'],
      ['project.list_json', 'project.list'],
      ['project.rm', 'project.rm'],
      ['project.shell_exec', 'project.shell_exec'],
    ]
    it.each(cases)('%s -> %s', (name, want) => {
      expect(inferToolNameFromInput(undefined, name)).toBe(want)
    })
  })

  describe('legacy dotted IDs (project.file_*)', () => {
    const cases: Array<[string, string]> = [
      ['project.file_read', 'project.read'],
      ['project.file_read_base64', 'project.read_base64'],
      ['project.file_read_chunk', 'project.read_chunk'],
      ['project.file_write', 'project.write'],
      ['project.file_edit', 'project.edit'],
      ['project.file_glob', 'project.glob'],
      ['project.file_grep', 'project.grep'],
      ['project.file_list', 'project.list'],
      ['project.file_list_json', 'project.list'],
      ['project.file_rm', 'project.rm'],
    ]
    it.each(cases)('%s -> %s', (name, want) => {
      expect(inferToolNameFromInput(undefined, name)).toBe(want)
    })
  })

  describe('underscore aliases', () => {
    const cases: Array<[string, string]> = [
      ['project_read', 'project.read'],
      ['project_read_base64', 'project.read_base64'],
      ['project_edit', 'project.edit'],
      ['project_list_json', 'project.list'],
      ['project_shell_exec', 'project.shell_exec'],
      ['project_file_read', 'project.read'],
      ['project_file_edit', 'project.edit'],
      ['project_file_list_json', 'project.list'],
      ['project_file_grep', 'project.grep'],
    ]
    it.each(cases)('%s -> %s', (name, want) => {
      expect(inferToolNameFromInput(undefined, name)).toBe(want)
    })
  })

  describe('hyphen aliases (provider-facing dot -> hyphen folding)', () => {
    const cases: Array<[string, string]> = [
      ['project-read', 'project.read'],
      ['project-write', 'project.write'],
      ['project-shell_exec', 'project.shell_exec'],
      ['project-file_read', 'project.read'],
      ['project-file_read_base64', 'project.read_base64'],
      ['project-file_edit', 'project.edit'],
      ['project-file_write', 'project.write'],
      ['project-file_grep', 'project.grep'],
      ['project-file_glob', 'project.glob'],
      ['project-file_list_json', 'project.list'],
      ['project-file_rm', 'project.rm'],
    ]
    it.each(cases)('%s -> %s', (name, want) => {
      expect(inferToolNameFromInput(undefined, name)).toBe(want)
    })
  })

  describe('bare names', () => {
    const cases: Array<[string, string]> = [
      ['read', 'project.read'],
      ['write', 'project.write'],
      ['edit', 'project.edit'],
      ['glob', 'project.glob'],
      ['grep', 'project.grep'],
      ['list', 'project.list'],
      ['list_json', 'project.list'],
      ['rm', 'project.rm'],
      ['shell_exec', 'project.shell_exec'],
      ['file_read', 'project.read'],
      ['file_edit', 'project.edit'],
    ]
    it.each(cases)('%s -> %s', (name, want) => {
      expect(inferToolNameFromInput(undefined, name)).toBe(want)
    })
  })

  describe('SSH tool name inference', () => {
    const runCases: Array<[string, string]> = [
      ['sshmanager.shell_run', 'sshmanager.shell_run'],
      ['sshmanager-shell_run', 'sshmanager.shell_run'],
      ['shell_run', 'sshmanager.shell_run'],
      ['ssh_run', 'sshmanager.shell_run'],
    ]
    it.each(runCases)('shell_run variant %s -> %s (no input)', (name, want) => {
      expect(inferToolNameFromInput(undefined, name)).toBe(want)
    })

    const openCases: Array<[string, string]> = [
      ['sshmanager.shell_open', 'sshmanager.shell_open'],
      ['sshmanager-shell_open', 'sshmanager.shell_open'],
      ['shell_open', 'sshmanager.shell_open'],
      ['ssh_open', 'sshmanager.shell_open'],
    ]
    it.each(openCases)('shell_open variant %s -> %s (no input)', (name, want) => {
      expect(inferToolNameFromInput(undefined, name)).toBe(want)
    })

    it('ssh shell_run with Command field is not misidentified as project.shell_exec', () => {
      // The bug: when the fallback is a hyphenated SSH name and the input
      // JSON carries a Command field, the old code fell through to the
      // rec.Command != null branch and returned project.shell_exec.
      expect(inferToolNameFromInput('{"Command":"ls","SessionId":"abc"}', 'sshmanager-shell_run')).toBe('sshmanager.shell_run')
    })

    it('ssh shell_run dotted name with Command field still wins', () => {
      expect(inferToolNameFromInput('{"Command":"whoami"}', 'sshmanager.shell_run')).toBe('sshmanager.shell_run')
    })

    it('ssh shell_run bare name with Command field still wins', () => {
      expect(inferToolNameFromInput('{"Command":"pwd"}', 'shell_run')).toBe('sshmanager.shell_run')
    })

    it('ssh shell_open with HostId field is not misidentified', () => {
      expect(inferToolNameFromInput('{"HostId":"my-server"}', 'sshmanager-shell_open')).toBe('sshmanager.shell_open')
    })

    it('generic fallback with Command field still infers project.shell_exec', () => {
      // Non-SSH fallback names with a Command field should still resolve
      // to project.shell_exec — the SSH fix only affects SSH-named tools.
      expect(inferToolNameFromInput('{"command":"ls"}', 'tool_call')).toBe('project.shell_exec')
    })
  })

  it('canonicalizes even when input is present (fallback wins)', () => {
    expect(inferToolNameFromInput('{"path":"/tmp"}', 'project-file_read')).toBe('project.read')
    expect(inferToolNameFromInput('{"path":"/"}', 'project.file_list_json')).toBe('project.list')
  })

  it('infers new IDs from raw input when the name is generic', () => {
    expect(inferToolNameFromInput('{"old_string":"a","new_string":"b"}', 'tool_call')).toBe('project.edit')
    expect(inferToolNameFromInput('{"content":"x"}', 'tool_call')).toBe('project.write')
    expect(inferToolNameFromInput('{"command":"ls"}', 'tool_call')).toBe('project.shell_exec')
    expect(inferToolNameFromInput('{"pattern":"foo"}', 'tool_call')).toBe('project.grep')
    expect(inferToolNameFromInput('{"path":"/tmp","depth":1}', 'tool_call')).toBe('project.list')
    expect(inferToolNameFromInput('{"path":"/tmp"}', 'tool_call')).toBe('project.read')
  })

  it('maps screenshot aliases to computeruse.screenshot', () => {
    expect(inferToolNameFromInput(undefined, 'screenshot')).toBe('computeruse.screenshot')
    expect(inferToolNameFromInput(undefined, 'computeruse_screenshot')).toBe('computeruse.screenshot')
  })

  it('leaves unrelated tool names untouched', () => {
    expect(inferToolNameFromInput(undefined, 'worktree_list')).toBe('worktree_list')
    expect(inferToolNameFromInput(undefined, 'mcp-filesystem-read_file')).toBe('mcp-filesystem-read_file')
    expect(inferToolNameFromInput(undefined, 'fork_explore')).toBe('fork_explore')
    expect(inferToolNameFromInput('not json', 'some_custom_tool')).toBe('some_custom_tool')
  })
})
