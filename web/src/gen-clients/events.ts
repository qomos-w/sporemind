// AUTO-GENERATED - DO NOT EDIT. To regenerate: make gen
export const Events = {
  agent: {
    agent_message_received: {
      actorPath: "agent",
      kind: "agent_message_received",
      schemaId: 275,
      schemaName: "AgentMessage",
    },
    step: {
      actorPath: "agent",
      kind: "step",
      schemaId: 340,
      schemaName: "StepEvent",
    },
    turn: {
      actorPath: "agent",
      kind: "turn",
      schemaId: 368,
      schemaName: "TurnEvent",
    },
  },
  appmanager: {
    app_event: {
      actorPath: "appmanager",
      kind: "app_event",
      schemaId: 2108,
      schemaName: "AppEventMessage",
    },
    app_lifecycle: {
      actorPath: "appmanager",
      kind: "app_lifecycle",
      schemaId: 2053,
      schemaName: "AppLifecycleEvent",
    },
  },
  browsermanager: {
    browser_manager_event: {
      actorPath: "browsermanager",
      kind: "browser_manager_event",
      schemaId: 588,
      schemaName: "BrowserManagerEvent",
    },
  },
  glassinteract: {
    "glass.event.completed": {
      actorPath: "glassinteract",
      kind: "glass.event.completed",
      schemaId: 3935,
      schemaName: "GlassEventCompletedEvent",
    },
    "glass.event.delivered": {
      actorPath: "glassinteract",
      kind: "glass.event.delivered",
      schemaId: 3934,
      schemaName: "GlassEventDeliveredEvent",
    },
    "glass.event.enqueued": {
      actorPath: "glassinteract",
      kind: "glass.event.enqueued",
      schemaId: 3933,
      schemaName: "GlassEventEnqueuedEvent",
    },
    "glass.hud.update": {
      actorPath: "glassinteract",
      kind: "glass.hud.update",
      schemaId: 3937,
      schemaName: "GlassHudStatus",
    },
    "glass.interaction": {
      actorPath: "glassinteract",
      kind: "glass.interaction",
      schemaId: 4036,
      schemaName: "GlassInteractionEvent",
    },
    "glass.offline": {
      actorPath: "glassinteract",
      kind: "glass.offline",
      schemaId: 3894,
      schemaName: "GlassLifecycleEvent",
    },
    "glass.online": {
      actorPath: "glassinteract",
      kind: "glass.online",
      schemaId: 3894,
      schemaName: "GlassLifecycleEvent",
    },
    "glass.reconnected": {
      actorPath: "glassinteract",
      kind: "glass.reconnected",
      schemaId: 3894,
      schemaName: "GlassLifecycleEvent",
    },
    "glass.render": {
      actorPath: "glassinteract",
      kind: "glass.render",
      schemaId: 3910,
      schemaName: "GlassRenderEvent",
    },
    "glass.replaced": {
      actorPath: "glassinteract",
      kind: "glass.replaced",
      schemaId: 3894,
      schemaName: "GlassLifecycleEvent",
    },
    "glass.speak": {
      actorPath: "glassinteract",
      kind: "glass.speak",
      schemaId: 3913,
      schemaName: "GlassSpeakEvent",
    },
    "glass.transcript": {
      actorPath: "glassinteract",
      kind: "glass.transcript",
      schemaId: 3905,
      schemaName: "GlassTranscriptEvent",
    },
  },
  interfacemanager: {
    interface_manager_event: {
      actorPath: "interfacemanager",
      kind: "interface_manager_event",
      schemaId: 3701,
      schemaName: "InterfaceManagerEvent",
    },
  },
  lspserver: {
    "lsp.diagnostics": {
      actorPath: "lspserver",
      kind: "lsp.diagnostics",
      schemaId: 5004,
      schemaName: "LspDiagnosticsEvent",
    },
    "lsp.install_progress": {
      actorPath: "lspserver",
      kind: "lsp.install_progress",
      schemaId: 5013,
      schemaName: "LspInstallProgressEvent",
    },
  },
  mcpmanager: {
    "mcp.server_status": {
      actorPath: "mcpmanager",
      kind: "mcp.server_status",
      schemaId: 4199,
      schemaName: "McpServerStatusEvent",
    },
  },
  oracle: {
    diagnostic: {
      actorPath: "oracle",
      kind: "diagnostic",
      schemaId: 949,
      schemaName: "Diagnostic",
    },
  },
  project: {
    card_changed: {
      actorPath: "project",
      kind: "card_changed",
      schemaId: 2353,
      schemaName: "WikiCardChangedEvent",
    },
    file_changed: {
      actorPath: "project",
      kind: "file_changed",
      schemaId: 1024,
      schemaName: "ProjectFileChangedEvent",
    },
    graph_changed: {
      actorPath: "project",
      kind: "graph_changed",
      schemaId: 2354,
      schemaName: "GraphChangedEvent",
    },
  },
  runtime: {
    "topology-epoch": {
      actorPath: "runtime",
      kind: "topology-epoch",
      schemaId: 894,
      schemaName: "TopologyEpochEvent",
    },
  },
  shell: {
    "shell.session_output": {
      actorPath: "shell",
      kind: "shell.session_output",
      schemaId: 5064,
      schemaName: "ShellSessionOutputEvent",
    },
  },
  sshmanager: {
    ssh_manager_event: {
      actorPath: "sshmanager",
      kind: "ssh_manager_event",
      schemaId: 1609,
      schemaName: "SshManagerEvent",
    },
  },
  toast: {
    "toast.action_triggered": {
      actorPath: "toast",
      kind: "toast.action_triggered",
      schemaId: 6218,
      schemaName: "ToastActionTriggeredEvent",
    },
    "toast.card_added": {
      actorPath: "toast",
      kind: "toast.card_added",
      schemaId: 6208,
      schemaName: "ToastCard",
    },
    "toast.card_removed": {
      actorPath: "toast",
      kind: "toast.card_removed",
      schemaId: 6213,
      schemaName: "ToastCardRemovedEvent",
    },
  },
  workspace: {
    agent_list_state: {
      actorPath: "workspace",
      kind: "agent_list_state",
      schemaId: 1819,
      schemaName: "WorkspaceAgentListStateEvent",
    },
    agents_changed: {
      actorPath: "workspace",
      kind: "agents_changed",
      schemaId: 1814,
      schemaName: "WorkspaceAgentsChangedEvent",
    },
    mounts: {
      actorPath: "workspace",
      kind: "mounts",
      schemaId: 1813,
      schemaName: "WorkspaceMountsEvent",
    },
    "workspace.log": {
      actorPath: "workspace",
      kind: "workspace.log",
      schemaId: 5105,
      schemaName: "WorkspaceLogStreamEvent",
    },
  },
} as const;
