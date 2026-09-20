import type { MonoCard } from './mono-types'

export type CardTemplate = 'note' | 'scheduler'

export const CARD_TEMPLATES: ReadonlyArray<{ id: CardTemplate; label: string }> = [
  { id: 'note', label: 'New card' },
  { id: 'scheduler', label: 'Scheduler card' },
]

export function cardTemplate(id: CardTemplate): Pick<MonoCard, 'id' | 'type' | 'tags' | 'list' | 'status' | 'data' | 'body'> {
  switch (id) {
    case 'scheduler':
      return {
        id: 'Scheduled task',
        type: 'scheduler',
        tags: ['scheduler'],
        list: [],
        data: { schedule: { cron: '' }, executor: '', reviewer: '', run_status: 'idle' },
        body: 'Describe the task for the executor agent.\n',
      }
    default:
      return { id: 'Untitled', type: 'wiki', tags: [], list: [], data: {}, body: '' }
  }
}
