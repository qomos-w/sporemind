import { describe, it, expect } from 'vitest'
import { getToolKind, buildToolDisplay, parseMCPToolName } from './tool-display.ts'
import { Trash2 } from 'lucide-react'
import enUS from '../../../../i18n/locales/en-US.json'
import type { I18nKey } from '../../../../i18n/types'

const t = (key: I18nKey) => (enUS as Record<string, string>)[key] ?? key

describe('getToolKind', () => {
  it('classifies explore as explore', () => {
    expect(getToolKind('agent.explore')).toBe('explore')
    expect(getToolKind('fork_explore')).toBe('explore')
  })

  it('classifies fork tools as fork', () => {
    expect(getToolKind('fork_agent')).toBe('fork')
    expect(getToolKind('fork_general')).toBe('fork')
  })

  it('classifies fork_review as review', () => {
    expect(getToolKind('fork_review')).toBe('review')
  })

  it('does not misclassify app tool names containing "cat" as read', () => {
    // authentiCATor-codes: the substring 'cat' must only match a standalone
    // "cat" segment, not words that merely contain it.
    expect(getToolKind('authenticator-codes')).toBe('generic')
    expect(getToolKind('cat')).toBe('read')
    expect(getToolKind('filesystem.cat')).toBe('read')
  })

  it('classifies subagent tools as fork', () => {
    expect(getToolKind('subagent')).toBe('fork')
    expect(getToolKind('sub_agent')).toBe('fork')
  })
  it('classifies task tools as task', () => {
    expect(getToolKind('create_task')).toBe('task')
    expect(getToolKind('update_task')).toBe('task')
    expect(getToolKind('task.create')).toBe('task')
  })

  it('classifies system introspection tools as search', () => {
    expect(getToolKind('list_callables')).toBe('search')
    expect(getToolKind('search_services')).toBe('search')
    expect(getToolKind('list_agents')).toBe('search')
    // project list callables stay as list (directory listing), not search
    expect(getToolKind('project.list_json')).toBe('list')
    expect(getToolKind('project_list')).toBe('list')
  })

  it('classifies mcp-prefixed tools as mcp', () => {
    expect(getToolKind('mcp.github.get_issue')).toBe('mcp')
    expect(getToolKind('mcp.srv_1.read_file')).toBe('mcp')
    // mcp. prefix wins over substring checks (read_file would match 'read')
    expect(getToolKind('mcp.files.read_file')).toBe('mcp')
    expect(getToolKind('MCP.GITHUB.GET_ISSUE')).toBe('mcp')
    // hyphen-separated LLM-facing form
    expect(getToolKind('mcp-filesystem-read_file')).toBe('mcp')
    expect(getToolKind('mcp-echo-server-echo')).toBe('mcp')
  })

  it('classifies show_page_thumbnail as page_preview', () => {
    expect(getToolKind('show_page_thumbnail')).toBe('page_preview')
    expect(getToolKind('agent.show_page_thumbnail')).toBe('page_preview')
  })

  it('classifies sshmanager shell_open and shell_run', () => {
    expect(getToolKind('sshmanager.shell_open')).toBe('ssh_session')
    expect(getToolKind('sshmanager.shell_run')).toBe('ssh_run')
  })

  it('classifies deletion tools as delete', () => {
    expect(getToolKind('project.rm')).toBe('delete')
    expect(getToolKind('rm')).toBe('delete')
    expect(getToolKind('project.file_rm')).toBe('delete')
    expect(getToolKind('project-file_rm')).toBe('delete')
    expect(getToolKind('filesystem.rm')).toBe('delete')
    expect(getToolKind('sshmanager.file_delete')).toBe('delete')
  })

  it('does not misclassify words containing rm as delete', () => {
    expect(getToolKind('confirm_plan')).toBe('generic')
    expect(getToolKind('normalize')).toBe('generic')
    // card tools keep their card view even when named delete_card
    expect(getToolKind('project.wiki_delete_card')).toBe('card')
  })
})

describe('parseMCPToolName', () => {
  it('splits hyphen-separated LLM names', () => {
    expect(parseMCPToolName('mcp-filesystem-read_file')).toEqual({ server: 'filesystem', tool: 'read_file' })
    expect(parseMCPToolName('mcp-echo-server-echo')).toEqual({ server: 'echo', tool: 'server-echo' })
  })
  it('still splits the legacy dotted form', () => {
    expect(parseMCPToolName('mcp.github.get_issue')).toEqual({ server: 'github', tool: 'get_issue' })
    expect(parseMCPToolName('mcp.srv-0.no.schema')).toEqual({ server: 'srv-0', tool: 'no.schema' })
  })
  it('returns undefined for non-mcp names', () => {
    expect(parseMCPToolName('project-file_read')).toEqual({ server: undefined, tool: undefined })
  })
})

describe('buildToolDisplay', () => {
  it('renders bash subtitle with full command and args', () => {
    const display = buildToolDisplay('project-shell_exec', JSON.stringify({ Command: 'go', Args: ['test', './...'] }), t)
    expect(display.title).toBe('Run command')
    expect(display.subtitle).toBe('go test ./...')
  })

  it('quotes bash args containing spaces in subtitle', () => {
    const display = buildToolDisplay('shell_exec', JSON.stringify({ Command: 'git', Args: ['commit', '-m', 'a b'] }), t)
    expect(display.subtitle).toBe('git commit -m "a b"')
  })

  it('keeps plain command string subtitle without args', () => {
    const display = buildToolDisplay('shell_exec', JSON.stringify({ command: 'go test ./...' }), t)
    expect(display.subtitle).toBe('go test ./...')
  })

  it('renders explore as Explore', () => {
    const display = buildToolDisplay('agent.explore', JSON.stringify({ prompt: 'analyze auth' }), t)
    expect(display.title).toBe('Explore')
    expect(display.subtitle).toBe('analyze auth')
  })

  it('renders fork_agent as Run Parallel', () => {
    const display = buildToolDisplay('fork_agent', JSON.stringify({ description: 'Fetch weather', prompt: 'curl wttr.in' }), t)
    expect(display.title).toBe('Run Parallel')
    expect(display.subtitle).toBe('Fetch weather')
  })

  it('renders fork_review as Review Goal', () => {
    const display = buildToolDisplay('fork_review', JSON.stringify({ ReviewText: 'Check tests' }), t)
    expect(display.title).toBe('Review goal')
    expect(display.subtitle).toBe('Check tests')
  })

  it('prefers description over prompt for fork subtitle', () => {
    const display = buildToolDisplay('fork_general', JSON.stringify({ prompt: 'curl wttr.in' }), t)
    expect(display.title).toBe('Run Parallel')
    expect(display.subtitle).toBe('curl wttr.in')
  })

  it('renders update_task subtitle as id → status when no subject', () => {
    const display = buildToolDisplay('update_task', JSON.stringify({ Id: 'task-3', Status: 'completed' }), t)
    expect(display.title).toBe('Update task')
    expect(display.subtitle).toBe('task-3 → completed')
  })

  it('renders agent_review subtitle with decision prefix', () => {
    const display = buildToolDisplay('workspace-agent_review', JSON.stringify({ AgentActorId: 'agent-xyz', Decision: 'approve' }), t)
    expect(display.title).toBe('Review agent')
    expect(display.subtitle).toBe('Approve · agent-xyz')
  })

  it('renders agent_review reject decision', () => {
    const display = buildToolDisplay('agent_review', JSON.stringify({ Decision: 'reject', TaskCardId: 'card-1' }), t)
    expect(display.subtitle).toBe('Reject · card-1')
  })

  it('classifies agent messaging tools before read/search matches', () => {
    expect(getToolKind('workspace.agent_send_message')).toBe('agent_message')
    expect(getToolKind('workspace.agent_read_message')).toBe('agent_message')
    expect(getToolKind('workspace-agent_send_message')).toBe('agent_message')
  })

  it('renders agent_message send display with target subtitle', () => {
    const display = buildToolDisplay('workspace.agent_send_message', JSON.stringify({ Text: 'hi', ToAgentId: 'Build Weaver#635e' }), t)
    expect(display.title).toBe('Send message')
    expect(display.subtitle).toBe('Build Weaver#635e')
    expect(display.kind).toBe('agent_message')
  })

  it('renders agent_message read display', () => {
    const display = buildToolDisplay('workspace.agent_read_message', JSON.stringify({ ToAgentId: 'Build Weaver#635e' }), t)
    expect(display.title).toBe('Read messages')
    expect(display.subtitle).toBe('Build Weaver#635e')
  })

  it('renders generic subtitle from invoke_callable service and call id', () => {
    const display = buildToolDisplay('invoke_callable', JSON.stringify({ Service: 'workspace', CallID: 'list_agents' }), t)
    expect(display.title).toBe('Invoke Callable')
    expect(display.subtitle).toBe('workspace · list_agents')
  })

  it('routes generate_image to media_gen with prompt subtitle', () => {
    const display = buildToolDisplay('generate_image', JSON.stringify({ Prompt: 'a red panda' }), t)
    expect(display.kind).toBe('media_gen')
    expect(display.title).toBe('Generate image')
    expect(display.subtitle).toBe('a red panda')
  })

  it('routes generate_video to media_gen', () => {
    const display = buildToolDisplay('generate_video', JSON.stringify({ Prompt: 'ocean waves' }), t)
    expect(display.kind).toBe('media_gen')
    expect(display.title).toBe('Generate video')
    expect(display.subtitle).toBe('ocean waves')
  })

  it('renders generic subtitle from memory_recall query', () => {
    const display = buildToolDisplay('memory_recall', JSON.stringify({ Query: 'auth design' }), t)
    expect(display.subtitle).toBe('auth design')
  })

  it('renders generic subtitle from inspect_actor actor path', () => {
    const display = buildToolDisplay('inspect_actor', JSON.stringify({ ActorPath: 'workspace' }), t)
    expect(display.subtitle).toBe('workspace')
  })

  it('renders generic subtitle from capture_profile profile', () => {
    const display = buildToolDisplay('capture_profile', JSON.stringify({ Profile: 'heap' }), t)
    expect(display.subtitle).toBe('heap')
  })

  it('renders generic subtitle from frontend_debug operation', () => {
    const display = buildToolDisplay('frontend_debug', JSON.stringify({ Operation: 'eval' }), t)
    expect(display.subtitle).toBe('eval')
  })

  it('renders generic subtitle from get_system_logs source filter', () => {
    const display = buildToolDisplay('get_system_logs', JSON.stringify({ Source: 'console', Level: 'error' }), t)
    expect(display.subtitle).toBe('console')
  })

  it('renders card set_status subtitle as id → status', () => {
    const display = buildToolDisplay('project-wiki_set_status', JSON.stringify({ Id: 'plan-1', Status: 'done' }), t)
    expect(display.title).toBe('Set card status')
    expect(display.subtitle).toBe('plan-1 → done')
  })

  it('renders card search subtitle from query when no id', () => {
    const display = buildToolDisplay('project-wiki_search_cards', JSON.stringify({ Query: 'auth flow' }), t)
    expect(display.subtitle).toBe('auth flow')
  })

  it('renders card set_task_dependencies subtitle from task id', () => {
    const display = buildToolDisplay('project-wiki_set_task_dependencies', JSON.stringify({ TaskId: 'task-7', MapId: 'm1' }), t)
    expect(display.subtitle).toBe('task-7')
  })

  it('renders create_task as Create task', () => {
    const display = buildToolDisplay('create_task', JSON.stringify({ subject: 'Fix auth' }), t)
    expect(display.title).toBe('Create task')
    expect(display.subtitle).toBe('Fix auth')
  })

  it('renders update_task as Update task', () => {
    const display = buildToolDisplay('update_task', JSON.stringify({ subject: 'Update middleware' }), t)
    expect(display.title).toBe('Update task')
    expect(display.subtitle).toBe('Update middleware')
  })

  it('renders project.rm as Delete file with path subtitle', () => {
    const display = buildToolDisplay('project.rm', JSON.stringify({ Path: 'src\\old.ts' }), t)
    expect(display.kind).toBe('delete')
    expect(display.title).toBe('Delete file')
    expect(display.subtitle).toBe('src/old.ts')
    expect(display.icon).toBe(Trash2)
  })

  it('renders mcp tool with server and tool name in subtitle', () => {
    const display = buildToolDisplay('mcp.github.get_issue', JSON.stringify({ owner: 'qomos-w', repo: 'sporemind' }), t)
    expect(display.title).toBe('MCP tool')
    expect(display.subtitle).toBe('github · get_issue')
  })

  it('keeps dotted remainder as tool name for mcp tools', () => {
    const display = buildToolDisplay('mcp.server_a.nested.tool', '{}', t)
    expect(display.title).toBe('MCP tool')
    expect(display.subtitle).toBe('server_a · nested.tool')
  })
})
