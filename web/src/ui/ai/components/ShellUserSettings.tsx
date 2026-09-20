import { UserPanel } from '../../panels/UserPanel'
import { useI18n } from '../../../i18n'
import {
  Card,
  CardHeader,
  CardTitle,
  CardDescription,
  CardContent,
} from '../../settings/shadcn/ui/card'
import './ShellUserSettings.css'

export function ShellUserSettings() {
  const { t } = useI18n()
  return (
    <Card className="gap-0 py-0">
      <CardHeader className="border-b border-border py-4">
        <CardTitle className="text-sm">{t('settings.account.title')}</CardTitle>
        <CardDescription>{t('settings.account.desc')}</CardDescription>
      </CardHeader>
      <CardContent className="py-5">
        <UserPanel />
      </CardContent>
    </Card>
  )
}