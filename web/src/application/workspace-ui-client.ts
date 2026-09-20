import { client } from './generated-client'
import { uiGet as getWorkspaceUI } from '../gen-clients/workspace/client'
import { uiSaveAiShell, uiSaveAiShell_meta, uiSaveDock, uiSaveDock_meta, uiSaveExplorer, uiSaveExplorer_meta, uiSaveLayout, uiSaveLayout_meta, uiSavePanels, uiSavePanels_meta, uiSaveProjectCardBrowser, uiSaveProjectCardBrowser_meta } from '../gen-clients/workspace/client'
import type {
  SaveWorkspaceAIShellCommand,
  SaveWorkspaceDockCommand,
  SaveWorkspaceExplorerCommand,
  SaveWorkspaceLayoutCommand,
  SaveWorkspacePanelsCommand,
  SaveWorkspaceProjectCardBrowserCommand,
} from '../gen-types/workspace'
import type {
  SaveWorkspaceAIShellCommand as ClientSaveWorkspaceAIShellCommand,
  SaveWorkspaceDockCommand as ClientSaveWorkspaceDockCommand,
  SaveWorkspaceExplorerCommand as ClientSaveWorkspaceExplorerCommand,
  SaveWorkspaceLayoutCommand as ClientSaveWorkspaceLayoutCommand,
  SaveWorkspacePanelsCommand as ClientSaveWorkspacePanelsCommand,
  SaveWorkspaceProjectCardBrowserCommand as ClientSaveWorkspaceProjectCardBrowserCommand,
  WorkspaceUIModel,
} from '../gen-clients/system/types'

export type WorkspaceUIModelSaveKind = 'Layout' | 'Panels' | 'Dock' | 'AiShell' | 'ProjectCardBrowser' | 'Explorer'

type WorkspaceUICommandPayload<K extends WorkspaceUIModelSaveKind> =
  K extends 'Layout' ? Pick<SaveWorkspaceLayoutCommand, 'Layout'>
  : K extends 'Panels' ? Pick<SaveWorkspacePanelsCommand, 'Panels'>
  : K extends 'Dock' ? Pick<SaveWorkspaceDockCommand, 'Dock'>
  : K extends 'AiShell' ? Pick<SaveWorkspaceAIShellCommand, 'AiShell'>
  : K extends 'Explorer' ? Pick<SaveWorkspaceExplorerCommand, 'Explorer'>
  : Pick<SaveWorkspaceProjectCardBrowserCommand, 'ProjectCardBrowser'>

type SaveWorkspaceUICommandByKind = {
  Layout: SaveWorkspaceLayoutCommand
  Panels: SaveWorkspacePanelsCommand
  Dock: SaveWorkspaceDockCommand
  AiShell: SaveWorkspaceAIShellCommand
  ProjectCardBrowser: SaveWorkspaceProjectCardBrowserCommand
  Explorer: SaveWorkspaceExplorerCommand
}

type ClientSaveWorkspaceUICommandByKind = {
  Layout: ClientSaveWorkspaceLayoutCommand
  Panels: ClientSaveWorkspacePanelsCommand
  Dock: ClientSaveWorkspaceDockCommand
  AiShell: ClientSaveWorkspaceAIShellCommand
  ProjectCardBrowser: ClientSaveWorkspaceProjectCardBrowserCommand
  Explorer: ClientSaveWorkspaceExplorerCommand
}

type WorkspaceUISaveSpec = {
  [K in WorkspaceUIModelSaveKind]: {
    requestID: string
    save: (command: ClientSaveWorkspaceUICommandByKind[K]) => Promise<WorkspaceUIModel>
    toClientCommand: (command: SaveWorkspaceUICommandByKind[K]) => ClientSaveWorkspaceUICommandByKind[K]
  }
}

const workspaceUISaveSpecs: WorkspaceUISaveSpec = {
  Layout: {
    requestID: uiSaveLayout_meta.callable,
    save: (command) => uiSaveLayout(client, command),
    toClientCommand: (command) => ({
      ...command,
      Layout: {
        ...command.Layout,
        ActiveViewId: command.Layout.ActiveViewId ?? '',
      },
    }),
  },
  Panels: {
    requestID: uiSavePanels_meta.callable,
    save: (command) => uiSavePanels(client, command),
    toClientCommand: (command) => ({
      ...command,
      Panels: {
        ...command.Panels,
        ZoneTabOrder: command.Panels.ZoneTabOrder ?? {},
      },
    }),
  },
  Dock: {
    requestID: uiSaveDock_meta.callable,
    save: (command) => uiSaveDock(client, command),
    toClientCommand: (command) => command,
  },
  AiShell: {
    requestID: uiSaveAiShell_meta.callable,
    save: (command) => uiSaveAiShell(client, command),
    toClientCommand: (command) => ({
      ...command,
      AiShell: {
        SidebarOrder: command.AiShell.SidebarOrder ?? [],
        SidebarPinned: command.AiShell.SidebarPinned ?? [],
        AgentOrder: command.AiShell.AgentOrder ?? [],
        LayoutJson: command.AiShell.LayoutJson ?? '',
        SelectedAgentId: command.AiShell.SelectedAgentId ?? '',
        PreviousAgentId: command.AiShell.PreviousAgentId ?? '',
      },
    }),
  },
  ProjectCardBrowser: {
    requestID: uiSaveProjectCardBrowser_meta.callable,
    save: (command) => uiSaveProjectCardBrowser(client, command),
    toClientCommand: (command) => ({
      ...command,
      ProjectCardBrowser: {
        SortMode: command.ProjectCardBrowser.SortMode ?? '',
      },
    }),
  },
  Explorer: {
    requestID: uiSaveExplorer_meta.callable,
    save: (command) => uiSaveExplorer(client, command),
    toClientCommand: (command) => ({
      ...command,
      Explorer: {
        ActiveProjectId: command.Explorer.ActiveProjectId ?? '',
        SelectedPath: command.Explorer.SelectedPath ?? '',
        ExpandedPaths: command.Explorer.ExpandedPaths ?? [],
      },
    }),
  },
}

let cachedModel: WorkspaceUIModel | null = null
let loadPromise: Promise<WorkspaceUIModel> | null = null
let writeQueue: Promise<void> = Promise.resolve()

export async function loadWorkspaceUIModel(): Promise<WorkspaceUIModel> {
  if (cachedModel) {
    return cachedModel
  }
  if (!loadPromise) {
    loadPromise = getWorkspaceUI(client)
      .then((model: WorkspaceUIModel) => {
        cachedModel = model
        return model
      })
      .finally(() => {
        loadPromise = null
      })
  }
  return loadPromise as Promise<WorkspaceUIModel>
}

export async function saveWorkspaceUIModel<K extends WorkspaceUIModelSaveKind>(kind: K, buildPayload: (model: WorkspaceUIModel) => WorkspaceUICommandPayload<K>): Promise<void> {
  writeQueue = writeQueue
    .then(async () => {
      const model = await loadWorkspaceUIModel()
      const spec = workspaceUISaveSpecs[kind]
      const base = {
        requestId: spec.requestID,
        workspaceId: model.WorkspaceId || 'default',
        version: model.Version || 1,
      }
      const command = { ...base, ...buildPayload(model) } as unknown as SaveWorkspaceUICommandByKind[K]
      cachedModel = await spec.save(spec.toClientCommand(command))
    })
    .catch(() => {
      // suppress
    })
  return writeQueue
}
