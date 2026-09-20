export type TabContextMenuLocale =
  | 'en-US'
  | 'zh-CN'
  | 'zh-TW'
  | 'ja-JP'
  | 'ko-KR'
  | 'fr-FR'
  | 'de-DE'
  | 'es-ES'
  | 'pt-BR'
  | 'ru-RU'

export interface TabContextMenuLabels {
  close: string
  closeOthers: string
  closeRight: string
}

const dictionary: Record<TabContextMenuLocale, TabContextMenuLabels> = {
  'en-US': { close: 'Close', closeOthers: 'Close Others', closeRight: 'Close Right' },
  'zh-CN': { close: '关闭', closeOthers: '关闭其他', closeRight: '关闭右侧' },
  'zh-TW': { close: '關閉', closeOthers: '關閉其他', closeRight: '關閉右側' },
  'ja-JP': { close: '閉じる', closeOthers: '他を閉じる', closeRight: '右側を閉じる' },
  'ko-KR': { close: '닫기', closeOthers: '다른 탭 닫기', closeRight: '오른쪽 탭 닫기' },
  'fr-FR': { close: 'Fermer', closeOthers: 'Fermer les autres', closeRight: 'Fermer à droite' },
  'de-DE': { close: 'Schließen', closeOthers: 'Andere schließen', closeRight: 'Rechte schließen' },
  'es-ES': { close: 'Cerrar', closeOthers: 'Cerrar otros', closeRight: 'Cerrar a la derecha' },
  'pt-BR': { close: 'Fechar', closeOthers: 'Fechar outros', closeRight: 'Fechar à direita' },
  'ru-RU': { close: 'Закрыть', closeOthers: 'Закрыть остальные', closeRight: 'Закрыть справа' },
}

export function getTabContextMenuLabels(locale: TabContextMenuLocale = 'en-US'): TabContextMenuLabels {
  return dictionary[locale] ?? dictionary['en-US']
}
