import { type ScheduleDraft, scheduleDraftIsDirty } from './scheduledTasks'
import { useI18n } from '../../../i18n'
import type { I18nKey } from '../../../i18n/types'
import {
  Button,
  Input,
  SelectRoot,
  SelectTrigger,
  SelectValue,
  SelectContent,
  SelectItem,
  SelectItemText,
} from '../../settings/shadcn/ui'

interface ScheduleCronEditorProps {
  draft: ScheduleDraft
  onDraftChange: (d: ScheduleDraft) => void
  onApply: () => void
  onCancel?: () => void
  saving?: boolean
  error?: string
  /** The cron the draft was seeded from. When provided, the save button only
   *  renders while the draft rebuilds to a different cron. Omit it for create
   *  flows where the button is the dialog's apply action. */
  originalCron?: string
  /** The expression the draft was seeded from. Together with `originalCron`
   *  this forms the effective baseline (`expression || cron`) so a card
   *  carrying both fields is never shown as phantom-dirty. */
  originalExpression?: string
}

const REPEAT_OPTIONS: { value: ScheduleDraft['kind']; key: I18nKey }[] = [
  { value: 'everyDay', key: 'scheduled.repeat.everyDay' },
  { value: 'weekdays', key: 'scheduled.repeat.weekdays' },
  { value: 'weekly', key: 'scheduled.repeat.weekly' },
  { value: 'monthly', key: 'scheduled.repeat.monthly' },
  { value: 'custom', key: 'scheduled.repeat.custom' },
]

/** The five cron fields shown as labeled, tooltip-backed segments in the
 *  "custom" repeat mode. Each carries a short label (the time period) and a
 *  hover explanation of valid values. */
const CRON_FIELDS = [
  { idx: 0, label: 'scheduled.cron.minute', help: 'scheduled.cron.minute.help', labelFallback: 'Minute', helpFallback: '0-59; * = every minute; supports , - /' },
  { idx: 1, label: 'scheduled.cron.hour', help: 'scheduled.cron.hour.help', labelFallback: 'Hour', helpFallback: '0-23; * = every hour; supports , - /' },
  { idx: 2, label: 'scheduled.cron.dom', help: 'scheduled.cron.dom.help', labelFallback: 'Day', helpFallback: '1-31; * = every day; supports , - /' },
  { idx: 3, label: 'scheduled.cron.month', help: 'scheduled.cron.month.help', labelFallback: 'Month', helpFallback: '1-12; * = every month; supports , - /' },
  { idx: 4, label: 'scheduled.cron.dow', help: 'scheduled.cron.dow.help', labelFallback: 'Week', helpFallback: '0-7 (0 and 7 = Sunday); * = every day' },
] as const

/** Split a raw cron string into its five positional fields. Splits on single
 *  spaces (not /\s+/) so a cleared field keeps its position instead of
 *  collapsing the remaining fields left. Tolerates undefined input (partial
 *  drafts omit the field).
 *
 *  A stored cron that does not carry exactly five fields is reported as
 *  `invalid` instead of being silently padded with '*' — padding fabricated
 *  segments the editor then let the user "save" as a different expression,
 *  masking that the stored value was malformed. */
function parseCronParts(custom: string | undefined): { parts: string[]; invalid: boolean } {
  if (!custom || custom.trim() === '') return { parts: ['*', '*', '*', '*', '*'], invalid: false }
  const split = custom.split(' ')
  if (split.length !== 5) return { parts: split, invalid: true }
  return { parts: split, invalid: false }
}

export function ScheduleCronEditor({ draft, onDraftChange, onApply, onCancel, saving, error, originalCron, originalExpression }: ScheduleCronEditorProps) {
  const { t } = useI18n()

  const { parts: cronParts, invalid: cronInvalid } = parseCronParts(draft.custom)
  const onCronFieldChange = (idx: number, value: string) => {
    const next = cronParts.map((p, i) => (i === idx ? value : p))
    onDraftChange({ ...draft, custom: next.join(' ') })
  }

  const showSave = originalCron === undefined || scheduleDraftIsDirty(draft, originalCron, originalExpression)
  // A malformed custom cron surfaces immediately (not only after a rejected
  // save) so the user sees why the fields don't round-trip.
  const showError = draft.kind === 'custom' && cronInvalid
  const displayError = showError ? t('scheduled.edit.invalid') : error

  return (
    <div className="scheduled-schedule-editor" role="form">
      <label className="flex flex-col gap-1.5">
        <span className="text-sm font-medium">{t('scheduled.edit.repeat')}</span>
        <SelectRoot
          value={draft.kind}
          onValueChange={(value) => onDraftChange({ ...draft, kind: value as ScheduleDraft['kind'] })}
        >
          <SelectTrigger className="w-full" data-guide-id="scheduled-editor-repeat-trigger">
            <SelectValue>
              {t(
                REPEAT_OPTIONS.find((opt) => opt.value === draft.kind)?.key as I18nKey,
                { defaultValue: draft.kind },
              )}
            </SelectValue>
          </SelectTrigger>
          <SelectContent>
            {REPEAT_OPTIONS.map((opt) => (
              <SelectItem key={opt.value} value={opt.value} data-guide-id={`scheduled-editor-repeat-${opt.value}`}>
                <SelectItemText>{t(opt.key, { defaultValue: opt.value })}</SelectItemText>
              </SelectItem>
            ))}
          </SelectContent>
        </SelectRoot>
      </label>

      {draft.kind === 'weekly' && (
        <label className="flex flex-col gap-1.5">
          <span className="text-sm font-medium">{t('scheduled.edit.dayOfWeek')}</span>
          <SelectRoot
            value={String(draft.dow)}
            onValueChange={(value) => onDraftChange({ ...draft, dow: Number(value) })}
          >
            <SelectTrigger className="w-full" data-guide-id="scheduled-editor-dow-trigger">
              <SelectValue>
                {t(`scheduled.dow.${draft.dow === 0 ? 7 : draft.dow}` as I18nKey, { defaultValue: '' })}
              </SelectValue>
            </SelectTrigger>
            <SelectContent>
              {[0, 1, 2, 3, 4, 5, 6].map((d) => {
                const label = t(`scheduled.dow.${d === 0 ? 7 : d}` as I18nKey, { defaultValue: '' })
                return (
                  <SelectItem key={d} value={String(d)} data-guide-id={`scheduled-editor-dow-${d}`}>
                    <SelectItemText>{label}</SelectItemText>
                  </SelectItem>
                )
              })}
            </SelectContent>
          </SelectRoot>
        </label>
      )}

      {draft.kind === 'monthly' && (
        <label className="flex flex-col gap-1.5">
          <span className="text-sm font-medium">{t('scheduled.edit.dayOfMonth')}</span>
          <SelectRoot
            value={String(draft.dom)}
            onValueChange={(value) => onDraftChange({ ...draft, dom: Number(value) })}
          >
            <SelectTrigger className="w-full" data-guide-id="scheduled-editor-dom-trigger">
              <SelectValue>{draft.dom}</SelectValue>
            </SelectTrigger>
            <SelectContent>
              {Array.from({ length: 31 }, (_, i) => i + 1).map((n) => (
                <SelectItem key={n} value={String(n)} data-guide-id={`scheduled-editor-dom-${n}`}>
                  <SelectItemText>{n}</SelectItemText>
                </SelectItem>
              ))}
            </SelectContent>
          </SelectRoot>
        </label>
      )}

      {draft.kind === 'custom' ? (
        <div className="scheduled-editor-cron flex flex-col gap-1.5">
          <span className="text-sm font-medium">{t('scheduled.cron.title' as I18nKey, { defaultValue: 'Cron expression' })}</span>
          <div className="flex gap-2">
            {CRON_FIELDS.map((f) => (
              <label key={f.idx} className="flex flex-col gap-0.5 flex-1" title={t(f.help as I18nKey, { defaultValue: f.helpFallback })}>
                <span className="text-xs text-muted-foreground">{t(f.label as I18nKey, { defaultValue: f.labelFallback })}</span>
                <Input
                  type="text"
                  data-guide-id={`scheduled-cron-field-${f.idx}`}
                  value={cronParts[f.idx] ?? ''}
                  onChange={(e) => onCronFieldChange(f.idx, e.target.value)}
                />
              </label>
            ))}
          </div>
        </div>
      ) : (
        <label className="flex flex-col gap-1.5">
          <span className="text-sm font-medium">{t('scheduled.edit.time')}</span>
          <Input
            type="time"
            value={draft.time}
            onChange={(e) => onDraftChange({ ...draft, time: e.target.value })}
          />
        </label>
      )}

      {displayError && <div className="scheduled-editor-error">{displayError}</div>}
      <div className="scheduled-editor-actions">
        {showSave && (
          <Button
            type="button"
            className="scheduled-editor-btn primary"
            disabled={saving}
            onClick={onApply}
          >
            {t('scheduled.edit.save')}
          </Button>
        )}
        {onCancel && (
          <Button
            type="button"
            variant="outline"
            className="scheduled-editor-btn"
            disabled={saving}
            onClick={onCancel}
          >
            {t('scheduled.edit.cancel')}
          </Button>
        )}
      </div>
    </div>
  )
}
