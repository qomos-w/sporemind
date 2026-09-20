// One-off: inject onboarding.coordinator.* i18n keys into all 10 locale files.
const fs = require('fs')
const path = require('path')

const dir = path.join(__dirname, '..', 'web', 'src', 'i18n', 'locales')

const locales = {
  'en-US': {
    'onboarding.coordinator.title': 'Set up your assistant',
    'onboarding.coordinator.subtitle': 'Create a coordinator agent to start chatting.',
    'onboarding.coordinator.create': 'Create assistant',
    'onboarding.coordinator.nickname.label': 'Assistant nickname',
    'onboarding.coordinator.nickname.placeholder': 'e.g. Nova',
    'onboarding.coordinator.nickname.hint': 'This name will later let you call out this assistant by name.',
    'onboarding.coordinator.unit.label': 'Model',
    'onboarding.coordinator.unit.auto': 'Auto',
    'onboarding.coordinator.unit.aggregatorTag': 'Aggregator',
    'onboarding.coordinator.unit.empty': 'No models available yet. Add a provider first.',
  },
  'zh-CN': {
    'onboarding.coordinator.title': '设置你的助手',
    'onboarding.coordinator.subtitle': '创建一个 coordinator 助手即可开始对话。',
    'onboarding.coordinator.create': '创建助手',
    'onboarding.coordinator.nickname.label': '助手昵称',
    'onboarding.coordinator.nickname.placeholder': '例如 Nova',
    'onboarding.coordinator.nickname.hint': '此昵称未来可用于按名字呼叫该助手。',
    'onboarding.coordinator.unit.label': '模型',
    'onboarding.coordinator.unit.auto': '自动',
    'onboarding.coordinator.unit.aggregatorTag': '聚合器',
    'onboarding.coordinator.unit.empty': '暂无可用模型，请先添加 provider。',
  },
  'zh-TW': {
    'onboarding.coordinator.title': '設定你的助手',
    'onboarding.coordinator.subtitle': '建立一個 coordinator 助手即可開始對話。',
    'onboarding.coordinator.create': '建立助手',
    'onboarding.coordinator.nickname.label': '助手暱稱',
    'onboarding.coordinator.nickname.placeholder': '例如 Nova',
    'onboarding.coordinator.nickname.hint': '此暱稱未來可用於以名字呼叫該助手。',
    'onboarding.coordinator.unit.label': '模型',
    'onboarding.coordinator.unit.auto': '自動',
    'onboarding.coordinator.unit.aggregatorTag': '聚合器',
    'onboarding.coordinator.unit.empty': '暫無可用模型，請先新增 provider。',
  },
  'ja-JP': {
    'onboarding.coordinator.title': 'アシスタントを設定',
    'onboarding.coordinator.subtitle': 'coordinator アシスタントを作成すると会話を始められます。',
    'onboarding.coordinator.create': 'アシスタントを作成',
    'onboarding.coordinator.nickname.label': 'アシスタントのニックネーム',
    'onboarding.coordinator.nickname.placeholder': '例: Nova',
    'onboarding.coordinator.nickname.hint': 'この名前で、後でアシスタントを呼び出せるようになります。',
    'onboarding.coordinator.unit.label': 'モデル',
    'onboarding.coordinator.unit.auto': '自動',
    'onboarding.coordinator.unit.aggregatorTag': 'アグリゲータ',
    'onboarding.coordinator.unit.empty': '利用可能なモデルがありません。先にプロバイダを追加してください。',
  },
  'ko-KR': {
    'onboarding.coordinator.title': '어시스턴트 설정',
    'onboarding.coordinator.subtitle': 'coordinator 어시스턴트를 만들면 대화를 시작할 수 있습니다.',
    'onboarding.coordinator.create': '어시스턴트 만들기',
    'onboarding.coordinator.nickname.label': '어시스턴트 별명',
    'onboarding.coordinator.nickname.placeholder': '예: Nova',
    'onboarding.coordinator.nickname.hint': '이 이름으로 나중에 어시스턴트를 호출할 수 있습니다.',
    'onboarding.coordinator.unit.label': '모델',
    'onboarding.coordinator.unit.auto': '자동',
    'onboarding.coordinator.unit.aggregatorTag': '애그리게이터',
    'onboarding.coordinator.unit.empty': '사용 가능한 모델이 없습니다. 먼저 프로바이더를 추가하세요.',
  },
  'fr-FR': {
    'onboarding.coordinator.title': 'Configurez votre assistant',
    'onboarding.coordinator.subtitle': 'Créez un assistant coordinator pour discuter.',
    'onboarding.coordinator.create': 'Créer l’assistant',
    'onboarding.coordinator.nickname.label': 'Surnom de l’assistant',
    'onboarding.coordinator.nickname.placeholder': 'ex. Nova',
    'onboarding.coordinator.nickname.hint': 'Ce nom permettra plus tard d’appeler cet assistant par son nom.',
    'onboarding.coordinator.unit.label': 'Modèle',
    'onboarding.coordinator.unit.auto': 'Auto',
    'onboarding.coordinator.unit.aggregatorTag': 'Agrégateur',
    'onboarding.coordinator.unit.empty': 'Aucun modèle disponible. Ajoutez d’abord un fournisseur.',
  },
  'de-DE': {
    'onboarding.coordinator.title': 'Richte deinen Assistenten ein',
    'onboarding.coordinator.subtitle': 'Erstelle einen Coordinator-Assistenten, um zu chatten.',
    'onboarding.coordinator.create': 'Assistent erstellen',
    'onboarding.coordinator.nickname.label': 'Spitzname des Assistenten',
    'onboarding.coordinator.nickname.placeholder': 'z. B. Nova',
    'onboarding.coordinator.nickname.hint': 'Mit diesem Namen kannst du den Assistenten später beim Namen rufen.',
    'onboarding.coordinator.unit.label': 'Modell',
    'onboarding.coordinator.unit.auto': 'Automatisch',
    'onboarding.coordinator.unit.aggregatorTag': 'Aggregator',
    'onboarding.coordinator.unit.empty': 'Noch keine Modelle verfügbar. Füge zuerst einen Anbieter hinzu.',
  },
  'es-ES': {
    'onboarding.coordinator.title': 'Configura tu asistente',
    'onboarding.coordinator.subtitle': 'Crea un asistente coordinator para empezar a chatear.',
    'onboarding.coordinator.create': 'Crear asistente',
    'onboarding.coordinator.nickname.label': 'Apodo del asistente',
    'onboarding.coordinator.nickname.placeholder': 'p. ej. Nova',
    'onboarding.coordinator.nickname.hint': 'Este nombre permitirá luego llamar a este asistente por su nombre.',
    'onboarding.coordinator.unit.label': 'Modelo',
    'onboarding.coordinator.unit.auto': 'Automático',
    'onboarding.coordinator.unit.aggregatorTag': 'Agregador',
    'onboarding.coordinator.unit.empty': 'Sin modelos disponibles. Añade primero un proveedor.',
  },
  'pt-BR': {
    'onboarding.coordinator.title': 'Configure seu assistente',
    'onboarding.coordinator.subtitle': 'Crie um assistente coordinator para começar a conversar.',
    'onboarding.coordinator.create': 'Criar assistente',
    'onboarding.coordinator.nickname.label': 'Apelido do assistente',
    'onboarding.coordinator.nickname.placeholder': 'ex. Nova',
    'onboarding.coordinator.nickname.hint': 'Esse nome permitirá depois chamar este assistente pelo nome.',
    'onboarding.coordinator.unit.label': 'Modelo',
    'onboarding.coordinator.unit.auto': 'Automático',
    'onboarding.coordinator.unit.aggregatorTag': 'Agregador',
    'onboarding.coordinator.unit.empty': 'Nenhum modelo disponível. Adicione um provedor primeiro.',
  },
  'ru-RU': {
    'onboarding.coordinator.title': 'Настройте ассистента',
    'onboarding.coordinator.subtitle': 'Создайте ассистента coordinator, чтобы начать общение.',
    'onboarding.coordinator.create': 'Создать ассистента',
    'onboarding.coordinator.nickname.label': 'Псевдоним ассистента',
    'onboarding.coordinator.nickname.placeholder': 'напр. Nova',
    'onboarding.coordinator.nickname.hint': 'Позже по этому имени можно будет вызывать ассистента.',
    'onboarding.coordinator.unit.label': 'Модель',
    'onboarding.coordinator.unit.auto': 'Авто',
    'onboarding.coordinator.unit.aggregatorTag': 'Агрегатор',
    'onboarding.coordinator.unit.empty': 'Нет доступных моделей. Сначала добавьте провайдера.',
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
