import type { TurnEnvelope } from './frame-types'

export interface AIPreviewScenario {
  id: string
  label: string
  description?: string
  states: TurnEnvelope[][]
}

const userEnvelope: TurnEnvelope = {
  id: 'preview-user-1',
  role: 'user',
  userContent: 'Preview every TurnEnvelope-supported UI component.',
  userAttachments: [
    { name: 'requirements.md', mimeType: 'text/markdown', sizeBytes: 4210 },
  ],
  userImages: [
    {
      url: 'data:image/svg+xml;utf8,<svg xmlns="http://www.w3.org/2000/svg" width="320" height="180"><rect width="100%" height="100%" rx="16" fill="%23222"/><text x="24" y="96" fill="white" font-size="20">User image</text></svg>',
      alt: 'User supplied image preview',
    },
  ],
  frames: [],
  timestamp: '14:52',
}

const allFramesState: TurnEnvelope[] = [
  userEnvelope,
  {
    id: 'preview-all-assistant-1',
    role: 'assistant',
    timestamp: '14:52',
    metadata: {
      actionCount: 5,
      elapsedSeconds: 12,
      model: 'Preview Fixture',
      usage: { inputTokens: 1200, outputTokens: 760, totalTokens: 1960 },
    },
    tasks: [
      { id: 't1', subject: 'Fix authentication bug in login flow', status: 'completed', activeForm: 'Fixing auth bug' },
      { id: 't2', subject: 'Update session middleware', status: 'completed', activeForm: 'Updating middleware' },
      { id: 't3', subject: 'Add token refresh logic', status: 'completed', activeForm: 'Adding token refresh' },
    ],
    frames: [
      {
        id: 'preview-all:reasoning',
        type: 'reasoning',
        status: 'completed',
        content: 'Inspect the requested preview surface and render every supported TurnEnvelope frame variant once.',
        durationSeconds: 3,
        inputTokens: 800,
        outputTokens: 420,
      },
      {
        id: 'preview-all:text',
        type: 'text',
        status: 'completed',
        content: 'This preview covers **text**, **reasoning**, **tool**, **sources**, **attachments**, **image**, **error**, and **ui** frames. Terminal output is rendered as a Bash tool variant.',
      },
      {
        id: 'preview-all:tool:read',
        type: 'tool',
        status: 'completed',
        toolName: 'Read',
        input: '{"file_path":"web/src/ui/ai/components/AIConversationPage.tsx"}',
        output: 'Read completed. AIConversationPage renders assistant turns from TurnEnvelope snapshots.',
        durationSeconds: 1,
        inputTokens: 1200,
        outputTokens: 760,
      },
      {
        id: 'preview-all:sources',
        type: 'sources',
        status: 'completed',
        entries: [
          { title: 'Frame protocol', url: 'https://example.com/frame-protocol', snippet: 'Defines structured frame rendering for assistant turns.' },
          { title: 'Component checklist', snippet: 'Local design notes for AI shell preview coverage.' },
        ],
      },
      {
        id: 'preview-all:attachments',
        type: 'attachments',
        status: 'completed',
        files: [
          { name: 'analysis.json', mimeType: 'application/json', sizeBytes: 2048 },
          { name: 'patch.diff', mimeType: 'text/x-diff', sizeBytes: 8192 },
        ],
      },
      {
        id: 'preview-all:terminal',
        type: 'tool',
        status: 'completed',
        toolName: 'Bash',
        input: '{"command":"bun run build"}',
        output: '$ bun run build\n✓ TypeScript check passed\n✓ Vite production bundle created',
        exitCode: 0,
      },
      {
        id: 'preview-all:image',
        type: 'image',
        status: 'completed',
        url: 'data:image/svg+xml;utf8,<svg xmlns="http://www.w3.org/2000/svg" width="480" height="240"><rect width="100%" height="100%" rx="18" fill="%23182733"/><circle cx="120" cy="120" r="52" fill="%237c9cff"/><text x="200" y="128" fill="white" font-size="24">Generated image frame</text></svg>',
        alt: 'Generated image frame preview',
      },
      {
        id: 'preview-all:error',
        type: 'error',
        status: 'error',
        code: 'PREVIEW_SAMPLE_ERROR',
        message: 'This error frame is intentionally included to verify error block layout.',
      },
      {
        id: 'preview-all:ui',
        type: 'ui',
        status: 'completed',
        animation: 'fade',
        html: `
          <div style="border:1px solid var(--border-subtle); border-radius:12px; padding:12px; background:var(--bg-surface);">
            <div style="font-size:12px; color:var(--text-tertiary); margin-bottom:6px;">Dynamic UI frame</div>
            <div style="font-size:14px; color:var(--text-primary); font-weight:600;">Custom HTML payload</div>
            <div style="font-size:13px; color:var(--text-secondary); margin-top:4px;">Rendered through UIFrameBlock and TransitionHost.</div>
          </div>
        `,
      },
      {
        id: 'preview-all:ask_user',
        type: 'ask_user',
        status: 'running',
        questions: [
          {
            header: 'Auth method',
            question: 'Which authentication library should we use?',
            options: [
              { label: 'OAuth 2.0', description: 'Standard OAuth authorization flow', recommended: true },
              { label: 'JWT', description: 'JSON Web Token authentication' },
              { label: 'API Key', description: 'Simple key-based auth' },
            ],
            multiSelect: false,
          },
        ],
      },
    ],
  },
]

const statusMatrixInitial: TurnEnvelope[] = [
  userEnvelope,
  {
    id: 'preview-status-assistant-1',
    role: 'assistant',
    timestamp: '14:53',
    metadata: { actionCount: 2, elapsedSeconds: 4, model: 'Preview Fixture' },
    frames: [
      {
        id: 'preview-status:reasoning',
        type: 'reasoning',
        status: 'running',
        content: 'Evaluate status styling for running and pending frames.',
        inputTokens: 320,
        outputTokens: 180,
      },
      {
        id: 'preview-status:text',
        type: 'text',
        status: 'running',
        content: 'Streaming content is still being appended...',
      },
      {
        id: 'preview-status:tool',
        type: 'tool',
        status: 'pending',
        toolName: 'Bash',
        input: '{"command":"bun run build"}',
      },
      {
        id: 'preview-status:error',
        type: 'error',
        status: 'error',
        message: 'A sibling error frame should still render alongside running states.',
        code: 'PREVIEW_RUNNING_ERROR',
      },
    ],
  },
]

const statusMatrixCompleted: TurnEnvelope[] = [
  userEnvelope,
  {
    id: 'preview-status-assistant-1',
    role: 'assistant',
    timestamp: '14:53',
    metadata: { actionCount: 2, elapsedSeconds: 5, model: 'Preview Fixture', usage: { inputTokens: 400, outputTokens: 220, totalTokens: 620 } },
    frames: [
      {
        id: 'preview-status:reasoning',
        type: 'reasoning',
        status: 'completed',
        content: 'Evaluate status styling for running and pending frames.',
        durationSeconds: 2,
        inputTokens: 320,
        outputTokens: 180,
      },
      {
        id: 'preview-status:text',
        type: 'text',
        status: 'completed',
        content: 'Streaming content is still being appended... now completed.',
      },
      {
        id: 'preview-status:tool',
        type: 'tool',
        status: 'completed',
        toolName: 'Bash',
        input: '{"command":"bun run build"}',
        output: '✓ build completed',
        durationSeconds: 2,
        inputTokens: 400,
        outputTokens: 220,
      },
      {
        id: 'preview-status:error',
        type: 'error',
        status: 'error',
        message: 'A sibling error frame should still render alongside running states.',
        code: 'PREVIEW_RUNNING_ERROR',
      },
    ],
  },
]

const toolVariantsState: TurnEnvelope[] = [
  userEnvelope,
  {
    id: 'preview-tools-assistant-1',
    role: 'assistant',
    timestamp: '14:54',
    metadata: { actionCount: 4, elapsedSeconds: 6, model: 'Preview Fixture' },
    frames: [
      {
        id: 'preview-tools:bash',
        type: 'tool',
        status: 'completed',
        toolName: 'Bash',
        input: '{"command":"bun run build","cwd":"web"}',
        output: 'build ok',
        durationSeconds: 2,
        inputTokens: 850,
        outputTokens: 120,
      },
      {
        id: 'preview-tools:read',
        type: 'tool',
        status: 'completed',
        toolName: 'Read',
        input: '{"file_path":"web/src/ui/ai/parts/FrameRenderer.tsx"}',
        output: 'FrameRenderer handles all current frame variants.',
        durationSeconds: 1,
        inputTokens: 620,
        outputTokens: 340,
      },
      {
        id: 'preview-tools:search',
        type: 'tool',
        status: 'completed',
        toolName: 'Grep',
        input: '{"pattern":"FrameRenderer","path":"web/src/ui/ai/parts"}',
        output: '{"matches":["FrameRenderer.tsx:1","ToolCallBlock.tsx:104"]}',
        durationSeconds: 1,
        inputTokens: 480,
        outputTokens: 90,
      },
      {
        id: 'preview-tools:edit',
        type: 'tool',
        status: 'completed',
        toolName: 'Edit',
        input: `{"path":"web/src/ui/ai/model/ai-preview-envelopes.ts","old_string":"const defaultAIPreviewScenarioId = 'all-frames'","new_string":"const defaultAIPreviewScenarioId = 'tool-variants'"}`,
        output: `{"replacements":1,"additions":1,"deletions":1,"hunks":[{"old_start":460,"old_lines":1,"new_start":460,"new_lines":1,"lines":[" const defaultAIPreviewScenarioId = 'all-frames'","+const defaultAIPreviewScenarioId = 'tool-variants'","-const defaultAIPreviewScenarioId = 'all-frames']}]}`,
        durationSeconds: 2,
        inputTokens: 2100,
        outputTokens: 580,
        fileChanges: [
          {
            filename: 'ai-preview-envelopes.ts',
            filepath: 'web/src/ui/ai/model/ai-preview-envelopes.ts',
            icon: 'ts',
            additions: 1,
            deletions: 1,
            diffContent: '@@ -460,1 +460,1 @@\n-const defaultAIPreviewScenarioId = \'all-frames\'\n+const defaultAIPreviewScenarioId = \'tool-variants\'\n',
          },
        ],
      },
      {
        id: 'preview-tools:generic',
        type: 'tool',
        status: 'completed',
        toolName: 'CustomTool',
        input: '{"input":"example"}',
        output: 'Fallback rendering path.',
        durationSeconds: 1,
        inputTokens: 200,
        outputTokens: 60,
      },
    ],
  },
]

const mediaReferencesState: TurnEnvelope[] = [
  userEnvelope,
  {
    id: 'preview-media-assistant-1',
    role: 'assistant',
    timestamp: '14:55',
    metadata: { actionCount: 1, elapsedSeconds: 3, model: 'Preview Fixture' },
    frames: [
      {
        id: 'preview-media:text',
        type: 'text',
        status: 'completed',
        content: 'This scenario focuses on media-heavy content and reference-oriented frames.',
      },
      {
        id: 'preview-media:sources',
        type: 'sources',
        status: 'completed',
        entries: [
          { title: 'Design note', snippet: 'References can render with or without URLs.' },
          { title: 'Spec link', url: 'https://example.com/spec', snippet: 'Preview link example.' },
        ],
      },
      {
        id: 'preview-media:attachments',
        type: 'attachments',
        status: 'completed',
        files: [
          { name: 'diagram.png', mimeType: 'image/png', sizeBytes: 120409 },
          { name: 'trace.log', mimeType: 'text/plain', sizeBytes: 8120 },
        ],
      },
      {
        id: 'preview-media:image',
        type: 'image',
        status: 'completed',
        url: 'data:image/svg+xml;utf8,<svg xmlns="http://www.w3.org/2000/svg" width="420" height="220"><rect width="100%" height="100%" rx="18" fill="%230f172a"/><text x="36" y="110" fill="white" font-size="28">Preview image</text></svg>',
        alt: 'Preview media image',
      },
    ],
  },
]

const uiReplacementV1: TurnEnvelope[] = [
  userEnvelope,
  {
    id: 'preview-ui-assistant-1',
    role: 'assistant',
    timestamp: '14:56',
    metadata: { actionCount: 1, elapsedSeconds: 2, model: 'Preview Fixture' },
    frames: [
      {
        id: 'preview-ui:text',
        type: 'text',
        status: 'completed',
        content: 'The card below will be replaced in place using the same frame id.',
      },
      {
        id: 'preview-ui:card',
        type: 'ui',
        status: 'completed',
        animation: 'fade',
        html: `
          <div style="border:1px solid var(--border-subtle); border-radius:12px; padding:12px; background:var(--bg-surface);">
            <div style="font-size:12px; color:var(--text-tertiary); margin-bottom:6px;">Preview · v1</div>
            <div style="font-size:14px; color:var(--text-primary); font-weight:600; margin-bottom:4px;">Dynamic UI frame</div>
            <div style="font-size:13px; color:var(--text-secondary);">Initial state for slot replacement testing.</div>
          </div>
        `,
      },
    ],
  },
]

const uiReplacementV2: TurnEnvelope[] = [
  userEnvelope,
  {
    id: 'preview-ui-assistant-1',
    role: 'assistant',
    timestamp: '14:56',
    metadata: { actionCount: 1, elapsedSeconds: 3, model: 'Preview Fixture' },
    frames: [
      {
        id: 'preview-ui:text',
        type: 'text',
        status: 'completed',
        content: 'The card below will be replaced in place using the same frame id.',
      },
      {
        id: 'preview-ui:card',
        type: 'ui',
        status: 'completed',
        animation: 'fade',
        html: `
          <div style="border:1px solid var(--border-subtle); border-radius:12px; padding:12px; background:var(--bg-surface); box-shadow:0 0 0 1px color-mix(in srgb, var(--accent-primary) 18%, transparent) inset;">
            <div style="font-size:12px; color:var(--text-tertiary); margin-bottom:6px;">Preview · v2</div>
            <div style="font-size:14px; color:var(--text-primary); font-weight:600; margin-bottom:4px;">Dynamic UI frame</div>
            <div style="font-size:13px; color:var(--text-secondary); margin-bottom:10px;">Updated state reusing the same frame id to verify in-place replacement and animation.</div>
            <div style="display:flex; gap:8px; flex-wrap:wrap;">
              <span style="font-size:12px; padding:2px 8px; border-radius:999px; background:var(--accent-primary-dim); color:var(--accent-primary);">Same frame id</span>
              <span style="font-size:12px; padding:2px 8px; border-radius:999px; background:var(--status-success-dim); color:var(--status-success);">Transition verified</span>
            </div>
          </div>
        `,
      },
    ],
  },
]

const streamingToolRunning: TurnEnvelope[] = [
  userEnvelope,
  {
    id: 'preview-streaming-assistant-1',
    role: 'assistant',
    timestamp: '14:57',
    metadata: { actionCount: 1, elapsedSeconds: 2, model: 'Preview Fixture' },
    frames: [
      {
        id: 'preview-streaming:tool',
        type: 'tool',
        status: 'running',
        toolName: 'Bash',
        input: '{"command":"bun run build"}',
        output: 'Scanning dependencies...\nCompiling src/index.ts',
      },
    ],
  },
]

const streamingToolCompleted: TurnEnvelope[] = [
  userEnvelope,
  {
    id: 'preview-streaming-assistant-1',
    role: 'assistant',
    timestamp: '14:57',
    metadata: { actionCount: 1, elapsedSeconds: 5, model: 'Preview Fixture', usage: { inputTokens: 600, outputTokens: 300, totalTokens: 900 } },
    frames: [
      {
        id: 'preview-streaming:tool',
        type: 'tool',
        status: 'completed',
        toolName: 'Bash',
        input: '{"command":"bun run build"}',
        output: 'Scanning dependencies...\nCompiling src/index.ts\nBuild completed successfully',
        durationSeconds: 4,
        inputTokens: 600,
        outputTokens: 300,
      },
    ],
  },
]

const webToolsState: TurnEnvelope[] = [
  userEnvelope,
  {
    id: 'preview-web-assistant-1',
    role: 'assistant',
    timestamp: '14:58',
    metadata: { actionCount: 2, elapsedSeconds: 4, model: 'Preview Fixture' },
    frames: [
      {
        id: 'preview-web:fetch',
        type: 'tool',
        status: 'completed',
        toolName: 'web.fetch',
        input: '{"url":"https://example.com/docs","prompt":"Extract the API reference section"}',
        output: '{"url":"https://example.com/docs","code":200,"codeText":"OK","bytes":4210,"result":"# API Reference\\n\\n## GET /api/v1/items\\nReturns a list of items.\\n\\n### Parameters\\n- `limit` (int): Max items to return\\n- `offset` (int): Pagination offset","durationMs":340}',
        durationSeconds: 1,
        inputTokens: 120,
        outputTokens: 280,
      },
      {
        id: 'preview-web:search',
        type: 'tool',
        status: 'completed',
        toolName: 'web.search',
        input: '{"query":"gospore actor framework golang","limit":3}',
        output: '{"query":"gospore actor framework golang","results":[{"title":"gospore - Actor framework for Go","url":"https://github.com/qomos-w/gospore","snippet":"A lightweight actor framework for Go with minimal boilerplate."},{"title":"Actor model in Go - Best practices","url":"https://example.com/go-actors","snippet":"How to structure actor-based systems in Go."},{"title":"Building distributed systems with actors","url":"https://example.com/distributed-actors","snippet":"Patterns for actor-based distributed computing."}],"durationMs":520}',
        durationSeconds: 1,
        inputTokens: 80,
        outputTokens: 340,
      },
    ],
  },
]

const compactionAutoState: TurnEnvelope[] = [
  {
    id: 'preview-compaction-auto-user-1',
    role: 'user',
    userContent: 'Analyze the full codebase and propose refactoring for the auth module.',
    frames: [],
    timestamp: '15:00',
  },
  {
    id: 'preview-compaction-auto-assistant-1',
    role: 'assistant',
    timestamp: '15:00',
    metadata: { actionCount: 3, elapsedSeconds: 18, model: 'Preview Fixture' },
    frames: [
      {
        id: 'preview-compaction-auto:reasoning',
        type: 'reasoning',
        status: 'completed',
        content: 'The user wants a comprehensive analysis of the auth module. I will read the relevant files first.',
        durationSeconds: 2,
        inputTokens: 1200,
        outputTokens: 340,
      },
      {
        id: 'preview-compaction-auto:tool-read',
        type: 'tool',
        status: 'completed',
        toolName: 'Read',
        input: '{"file_path":"pkg/auth/module.go"}',
        output: 'package auth\\n\\nfunc Login(...) { ... }\\nfunc RefreshToken(...) { ... }',
        durationSeconds: 1,
        inputTokens: 800,
        outputTokens: 420,
      },
      {
        id: 'preview-compaction-auto:tool-search',
        type: 'tool',
        status: 'completed',
        toolName: 'Grep',
        input: '{"pattern":"auth\\.","path":"pkg"}',
        output: '{"matches":["pkg/auth/login.go:42","pkg/auth/token.go:17","pkg/middleware/auth.go:88","pkg/service/user.go:203"]}',
        durationSeconds: 2,
        inputTokens: 600,
        outputTokens: 180,
      },
      {
        id: 'preview-compaction-auto:compaction',
        type: 'compaction',
        status: 'completed',
        trigger: 'auto',
        beforeTokens: 82400,
        afterTokens: 39300,
        contextWindowSize: 200000,
        beforeLayout: [
          { kind: 'cold', stepIndex: -1, tokens: 12000 },
          { kind: 'message', stepIndex: -1, tokens: 42000 },
          { kind: 'hot', stepIndex: -1, tokens: 8000 },
          { kind: 'message', stepIndex: -1, tokens: 20400 },
        ],
        afterLayout: [
          { kind: 'cold', stepIndex: -1, tokens: 12000 },
          { kind: 'summary', stepIndex: -1, tokens: 4300 },
          { kind: 'hot', stepIndex: -1, tokens: 8000 },
          { kind: 'message', stepIndex: -1, tokens: 15000 },
        ],
        rounds: [
          {
            compactedRanges: [{ segmentIndex: 1, tokens: 24000 }],
            afterLayout: [
              { kind: 'cold', stepIndex: -1, tokens: 12000 },
              { kind: 'summary', stepIndex: -1, tokens: 2800 },
              { kind: 'message', stepIndex: -1, tokens: 18000 },
              { kind: 'hot', stepIndex: -1, tokens: 8000 },
              { kind: 'message', stepIndex: -1, tokens: 20400 },
            ],
            sourceStartIndex: 0,
            sourceEndIndex: 99,
            level: 1,
            round: 1,
            compactedMessageCount: 50,
          },
          {
            compactedRanges: [
              { segmentIndex: 1, tokens: 2800 },
              { segmentIndex: 4, tokens: 12400 },
            ],
            afterLayout: [
              { kind: 'cold', stepIndex: -1, tokens: 12000 },
              { kind: 'summary', stepIndex: -1, tokens: 4300 },
              { kind: 'hot', stepIndex: -1, tokens: 8000 },
              { kind: 'message', stepIndex: -1, tokens: 15000 },
            ],
            sourceStartIndex: 0,
            sourceEndIndex: 199,
            level: 2,
            round: 2,
            compactedMessageCount: 100,
          },
        ],
        model: 'gpt-4o-mini',
      },
      {
        id: 'preview-compaction-auto:text',
        type: 'text',
        status: 'completed',
        content: 'After analyzing the codebase, here are my refactoring recommendations:\\n\\n1. **Extract middleware** into `pkg/middleware/auth.go` for reuse.\\n2. **Use JWT + refresh tokens** instead of session cookies.\\n3. **Add token refresh endpoint** at `/auth/refresh`.\\n4. **Unify error handling** with structured auth errors.',
      },
    ],
  },
]

const compactionUserState: TurnEnvelope[] = [
  {
    id: 'preview-compaction-user-user-1',
    role: 'user',
    userContent: '/compact',
    frames: [],
    timestamp: '15:05',
  },
  {
    id: 'preview-compaction-user-assistant-1',
    role: 'assistant',
    timestamp: '15:05',
    metadata: { actionCount: 1, elapsedSeconds: 6, model: 'Preview Fixture' },
    frames: [
      {
        id: 'preview-compaction-user:compaction',
        type: 'compaction',
        status: 'completed',
        trigger: 'user',
        beforeTokens: 56800,
        afterTokens: 24200,
        contextWindowSize: 128000,
        beforeLayout: [
          { kind: 'cold', stepIndex: -1, tokens: 10000 },
          { kind: 'summary', stepIndex: -1, tokens: 8200 },
          { kind: 'message', stepIndex: -1, tokens: 26000 },
          { kind: 'hot', stepIndex: -1, tokens: 6000 },
          { kind: 'message', stepIndex: -1, tokens: 6600 },
        ],
        afterLayout: [
          { kind: 'cold', stepIndex: -1, tokens: 10000 },
          { kind: 'summary', stepIndex: -1, tokens: 5200 },
          { kind: 'hot', stepIndex: -1, tokens: 6000 },
          { kind: 'message', stepIndex: -1, tokens: 3000 },
        ],
        rounds: [
          {
            compactedRanges: [{ segmentIndex: 2, tokens: 14000 }],
            afterLayout: [
              { kind: 'cold', stepIndex: -1, tokens: 10000 },
              { kind: 'summary', stepIndex: -1, tokens: 8200 },
              { kind: 'summary', stepIndex: -1, tokens: 5000 },
              { kind: 'message', stepIndex: -1, tokens: 12000 },
              { kind: 'hot', stepIndex: -1, tokens: 6000 },
              { kind: 'message', stepIndex: -1, tokens: 6600 },
            ],
            sourceStartIndex: 0,
            sourceEndIndex: 49,
            level: 1,
            round: 1,
            compactedMessageCount: 25,
          },
          {
            compactedRanges: [
              { segmentIndex: 1, tokens: 8200 },
              { segmentIndex: 5, tokens: 6600 },
            ],
            afterLayout: [
              { kind: 'cold', stepIndex: -1, tokens: 10000 },
              { kind: 'summary', stepIndex: -1, tokens: 5200 },
              { kind: 'hot', stepIndex: -1, tokens: 6000 },
              { kind: 'message', stepIndex: -1, tokens: 3000 },
            ],
            sourceStartIndex: 0,
            sourceEndIndex: 99,
            level: 2,
            round: 2,
            compactedMessageCount: 50,
          },
        ],
        model: 'gpt-4o-mini',
      },
    ],
  },
]

export const aiPreviewScenarios: AIPreviewScenario[] = [
  {
    id: 'all-frames',
    label: 'All frames',
    description: 'Render every TurnEnvelope-supported frame component at least once.',
    states: [allFramesState],
  },
  {
    id: 'status-matrix',
    label: 'Status matrix',
    description: 'Exercise pending, running, completed, and error states.',
    states: [statusMatrixInitial, statusMatrixCompleted],
  },
  {
    id: 'tool-variants',
    label: 'Tool variants',
    description: 'Exercise specialized tool renderers and generic fallback.',
    states: [toolVariantsState],
  },
  {
    id: 'media-and-references',
    label: 'Media & refs',
    description: 'Exercise sources, attachments, user media, and image frames.',
    states: [mediaReferencesState],
  },
  {
    id: 'ui-replacement',
    label: 'UI replacement',
    description: 'Exercise in-place replacement for the same UI frame id.',
    states: [uiReplacementV1, uiReplacementV2],
  },
  {
    id: 'streaming-tool',
    label: 'Streaming tool',
    description: 'Exercise incremental tool output streaming with output deltas.',
    states: [streamingToolRunning, streamingToolCompleted],
  },
  {
    id: 'web-tools',
    label: 'Web tools',
    description: 'Exercise web.fetch and web.search tool renderers.',
    states: [webToolsState],
  },
  {
    id: 'compaction-auto',
    label: 'Compaction (auto)',
    description: 'Auto-triggered context compression mid-turn when token budget is exceeded.',
    states: [compactionAutoState],
  },
  {
    id: 'compaction-user',
    label: 'Compaction (/compact)',
    description: 'User-initiated context compression via /compact command.',
    states: [compactionUserState],
  },
]

export const defaultAIPreviewScenarioId = 'all-frames'

export function getAIPreviewScenario(id: string): AIPreviewScenario {
  return aiPreviewScenarios.find((scenario) => scenario.id === id) ?? aiPreviewScenarios[0]!
}
