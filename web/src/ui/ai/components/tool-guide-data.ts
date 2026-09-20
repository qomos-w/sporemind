/**
 * Static tool guide registry — the data source for ToolGuideOverlay.
 *
 * Each entry has an `id` and i18n keys for title + description. The overlay
 * resolves the keys at render time via `useI18n().t`, so the data file itself
 * contains no translated strings — only key references.
 *
 * Tool items are curated from the composer input triggers and command
 * surfaces:
 *  - slash menu (`/`): commands, skills, mountable bundles & modes
 *    (AIComposer.tsx visibleSlashActions, AIConversationComposer componentActions)
 *  - card mention (`#`): project card reference (useCardMention)
 *  - backlog intercept (`!`): quick note → backlog card (AIShellLayout handleSend)
 *  - file mention (`$`): project file reference (AIComposer useFileMention)
 *  - agent mention (`@`): agent-chat link (AIComposer useAgentMention)
 *  - omnibox (Ctrl/⌘+K): global palette with quick actions (useAppOmnibox)
 *
 * To add a new tool card, append an entry here and add the corresponding
 * `toolGuide.tools.<id>.title` / `toolGuide.tools.<id>.description` keys
 * to `zh-CN.json` and `en-US.json` (and the other locale catalogs).
 */
import type { I18nKey } from '../../../i18n'

export interface ToolGuideEntry {
  /** Stable identifier for the tool entry. */
  id: string
  /** i18n key resolving to the tool's display title. */
  titleKey: I18nKey
  /** i18n key resolving to the tool's expanded description. */
  descriptionKey: I18nKey
}

/**
 * Ordered list of tool guide entries. The overlay renders them in this order;
 * each card is collapsed by default, showing only the title.
 */
export const toolGuideEntries: ToolGuideEntry[] = [
  {
    id: 'slash-commands',
    titleKey: 'toolGuide.tools.slashCommands.title',
    descriptionKey: 'toolGuide.tools.slashCommands.description',
  },
  {
    id: 'card-mention',
    titleKey: 'toolGuide.tools.cardMention.title',
    descriptionKey: 'toolGuide.tools.cardMention.description',
  },
  {
    id: 'quick-note',
    titleKey: 'toolGuide.tools.quickNote.title',
    descriptionKey: 'toolGuide.tools.quickNote.description',
  },
  {
    id: 'file-mention',
    titleKey: 'toolGuide.tools.fileMention.title',
    descriptionKey: 'toolGuide.tools.fileMention.description',
  },
  {
    id: 'at-mention',
    titleKey: 'toolGuide.tools.atMention.title',
    descriptionKey: 'toolGuide.tools.atMention.description',
  },
  {
    id: 'goal',
    titleKey: 'toolGuide.tools.goal.title',
    descriptionKey: 'toolGuide.tools.goal.description',
  },
  {
    id: 'workflow',
    titleKey: 'toolGuide.tools.workflow.title',
    descriptionKey: 'toolGuide.tools.workflow.description',
  },
  {
    id: 'mounts',
    titleKey: 'toolGuide.tools.mounts.title',
    descriptionKey: 'toolGuide.tools.mounts.description',
  },
  {
    id: 'omnibox',
    titleKey: 'toolGuide.tools.omnibox.title',
    descriptionKey: 'toolGuide.tools.omnibox.description',
  },
]
