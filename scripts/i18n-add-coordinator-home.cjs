// One-off: inject onboarding.coordinator.home.* i18n keys into all 10 locale files.
const fs = require('fs')
const path = require('path')

const dir = path.join(__dirname, '..', 'web', 'src', 'i18n', 'locales')

const locales = {
  'en-US': {
    'onboarding.coordinator.home.subtitle': 'Ask anything, or pick a suggestion to start.',
    'onboarding.coordinator.home.placeholder': 'Ask anything…',
    'onboarding.coordinator.home.noModel': 'No model available',
    'onboarding.coordinator.home.loadingModels': 'Loading models…',
  },
  'zh-CN': {
    'onboarding.coordinator.home.subtitle': '随便问点什么，或选择一个建议开始。',
    'onboarding.coordinator.home.placeholder': '随便问点什么…',
    'onboarding.coordinator.home.noModel': '暂无可用模型',
    'onboarding.coordinator.home.loadingModels': '正在加载模型…',
  },
  'zh-TW': {
    'onboarding.coordinator.home.subtitle': '隨便問點什麼，或選擇一個建議開始。',
    'onboarding.coordinator.home.placeholder': '隨便問點什麼…',
    'onboarding.coordinator.home.noModel': '暫無可用模型',
    'onboarding.coordinator.home.loadingModels': '正在載入模型…',
  },
  'ja-JP': {
    'onboarding.coordinator.home.subtitle': '何でも聞いてください。提案から始めることもできます。',
    'onboarding.coordinator.home.placeholder': '何でも聞いてください…',
    'onboarding.coordinator.home.noModel': '利用可能なモデルがありません',
    'onboarding.coordinator.home.loadingModels': 'モデルを読み込み中…',
  },
  'ko-KR': {
    'onboarding.coordinator.home.subtitle': '무엇이든 물어보거나, 제안을 선택해 시작하세요.',
    'onboarding.coordinator.home.placeholder': '무엇이든 물어보세요…',
    'onboarding.coordinator.home.noModel': '사용 가능한 모델이 없습니다',
    'onboarding.coordinator.home.loadingModels': '모델을 불러오는 중…',
  },
  'fr-FR': {
    'onboarding.coordinator.home.subtitle': 'Posez n’importe quelle question, ou choisissez une suggestion.',
    'onboarding.coordinator.home.placeholder': 'Posez votre question…',
    'onboarding.coordinator.home.noModel': 'Aucun modèle disponible',
    'onboarding.coordinator.home.loadingModels': 'Chargement des modèles…',
  },
  'de-DE': {
    'onboarding.coordinator.home.subtitle': 'Frag etwas, oder wähle einen Vorschlag.',
    'onboarding.coordinator.home.placeholder': 'Frag etwas…',
    'onboarding.coordinator.home.noModel': 'Kein Modell verfügbar',
    'onboarding.coordinator.home.loadingModels': 'Modelle werden geladen…',
  },
  'es-ES': {
    'onboarding.coordinator.home.subtitle': 'Pregunta lo que quieras, o elige una sugerencia.',
    'onboarding.coordinator.home.placeholder': 'Pregunta lo que quieras…',
    'onboarding.coordinator.home.noModel': 'Ningún modelo disponible',
    'onboarding.coordinator.home.loadingModels': 'Cargando modelos…',
  },
  'pt-BR': {
    'onboarding.coordinator.home.subtitle': 'Pergunte qualquer coisa, ou escolha uma sugestão.',
    'onboarding.coordinator.home.placeholder': 'Pergunte qualquer coisa…',
    'onboarding.coordinator.home.noModel': 'Nenhum modelo disponível',
    'onboarding.coordinator.home.loadingModels': 'Carregando modelos…',
  },
  'ru-RU': {
    'onboarding.coordinator.home.subtitle': 'Спросите что угодно или выберите подсказку.',
    'onboarding.coordinator.home.placeholder': 'Спросите что угодно…',
    'onboarding.coordinator.home.noModel': 'Нет доступных моделей',
    'onboarding.coordinator.home.loadingModels': 'Загрузка моделей…',
  },
}

for (const [file, keys] of Object.entries(locales)) {
  const full = path.join(dir, file + '.json')
  const data = JSON.parse(fs.readFileSync(full, 'utf8'))
  let added = 0
  for (const [k, v] of Object.entries(keys)) {
    if (!(k in data)) { data[k] = v; added++ }
  }
  fs.writeFileSync(full, JSON.stringify(data, null, 2) + '\n', 'utf8')
  console.log(`${file}: +${added}`)
}