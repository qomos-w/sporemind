import { registerInspectorComponent } from './inspectorRegistry'
import { AgentPromptPage } from './pages/AgentPromptPage'
import { CompiledPromptPage } from './pages/CompiledPromptPage'
import { TurnHistoryPage } from './pages/TurnHistoryPage'
import { CompactionSnapshotPage } from './pages/CompactionSnapshotPage'
import { ContextBudgetPage } from './pages/ContextBudgetPage'
import { ComponentSnapshotPage } from './pages/ComponentSnapshotPage'

registerInspectorComponent('prompt-context', AgentPromptPage)
registerInspectorComponent('compiled-prompt', CompiledPromptPage)
registerInspectorComponent('turn-history', TurnHistoryPage)
registerInspectorComponent('compaction-snapshot', CompactionSnapshotPage)
registerInspectorComponent('context-budget', ContextBudgetPage)
registerInspectorComponent('component-snapshot', ComponentSnapshotPage)
