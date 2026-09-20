// One-off: inject greeting phrases + selector label + newChat subtitle into all 10 locale files.
const fs = require('fs')
const path = require('path')

const dir = path.join(__dirname, '..', 'web', 'src', 'i18n', 'locales')

const locales = {
  'en-US': {
    'onboarding.coordinator.home.greeting.1': "I'm {name}, what can I do for you?",
    'onboarding.coordinator.home.greeting.2': "Hi, I'm {name}. What are we exploring today?",
    'onboarding.coordinator.home.greeting.3': "{name} here — how can I help?",
    'onboarding.coordinator.home.greeting.4': "I'm {name}. Ask me anything.",
    'onboarding.coordinator.home.greeting.5': "Hey, {name} at your service. What's on your mind?",
    'onboarding.coordinator.home.greeting.6': "I'm {name}. Let's get something done.",
    'onboarding.coordinator.home.selectorLabel': 'Home',
    'shell.newChat.subtitle': 'Start a new coding conversation',
  },
  'zh-CN': {
    'onboarding.coordinator.home.greeting.1': '我是 {name}，可以为你做什么？',
    'onboarding.coordinator.home.greeting.2': '你好，我是 {name}。今天想探索什么？',
    'onboarding.coordinator.home.greeting.3': '{name} 在此——有什么能帮到你的？',
    'onboarding.coordinator.home.greeting.4': '我是 {name}，随便问点什么吧。',
    'onboarding.coordinator.home.greeting.5': '嗨，{name} 随时为你效劳。在想什么？',
    'onboarding.coordinator.home.greeting.6': '我是 {name}，让我们开始吧。',
    'onboarding.coordinator.home.selectorLabel': '主页',
    'shell.newChat.subtitle': '开始一段新的编程对话',
  },
  'zh-TW': {
    'onboarding.coordinator.home.greeting.1': '我是 {name}，可以為你做什麼？',
    'onboarding.coordinator.home.greeting.2': '你好，我是 {name}。今天想探索什麼？',
    'onboarding.coordinator.home.greeting.3': '{name} 在此——有什麼能幫到你的？',
    'onboarding.coordinator.home.greeting.4': '我是 {name}，隨便問點什麼吧。',
    'onboarding.coordinator.home.greeting.5': '嗨，{name} 隨時為你效勞。在想什麼？',
    'onboarding.coordinator.home.greeting.6': '我是 {name}，讓我們開始吧。',
    'onboarding.coordinator.home.selectorLabel': '首頁',
    'shell.newChat.subtitle': '開始一段新的程式對話',
  },
  'ja-JP': {
    'onboarding.coordinator.home.greeting.1': '私は{name}です。何をお手伝いしましょうか？',
    'onboarding.coordinator.home.greeting.2': 'こんにちは、{name}です。今日は何を探求しますか？',
    'onboarding.coordinator.home.greeting.3': '{name}です — どうしましたか？',
    'onboarding.coordinator.home.greeting.4': '私は{name}。何でも聞いてください。',
    'onboarding.coordinator.home.greeting.5': 'やあ、{name}がお供します。何を考えていますか？',
    'onboarding.coordinator.home.greeting.6': '私は{name}。さあ、始めましょう。',
    'onboarding.coordinator.home.selectorLabel': 'ホーム',
    'shell.newChat.subtitle': '新しいコーディング会話を始める',
  },
  'ko-KR': {
    'onboarding.coordinator.home.greeting.1': '저는 {name}입니다. 무엇을 도와드릴까요?',
    'onboarding.coordinator.home.greeting.2': '안녕하세요, {name}입니다. 오늘 무엇을 탐색할까요?',
    'onboarding.coordinator.home.greeting.3': '{name}입니다 — 어떻게 도울까요?',
    'onboarding.coordinator.home.greeting.4': '저는 {name}입니다. 무엇이든 물어보세요.',
    'onboarding.coordinator.home.greeting.5': '안녕, {name}가 도와드릴게요. 무슨 생각을 하고 계세요?',
    'onboarding.coordinator.home.greeting.6': '저는 {name}입니다. 시작해 볼까요.',
    'onboarding.coordinator.home.selectorLabel': '홈',
    'shell.newChat.subtitle': '새로운 코딩 대화 시작',
  },
  'fr-FR': {
    'onboarding.coordinator.home.greeting.1': "Je suis {name}, que puis-je faire pour vous ?",
    'onboarding.coordinator.home.greeting.2': "Salut, c'est {name}. Qu'explorons-nous aujourd'hui ?",
    'onboarding.coordinator.home.greeting.3': "{name} à votre service — comment puis-je aider ?",
    'onboarding.coordinator.home.greeting.4': "Je suis {name}. Posez-moi n'importe quelle question.",
    'onboarding.coordinator.home.greeting.5': "Salut, {name} à votre écoute. À quoi pensez-vous ?",
    'onboarding.coordinator.home.greeting.6': "Je suis {name}. Mettons-nous au travail.",
    'onboarding.coordinator.home.selectorLabel': 'Accueil',
    'shell.newChat.subtitle': 'Démarrer une nouvelle conversation de codage',
  },
  'de-DE': {
    'onboarding.coordinator.home.greeting.1': 'Ich bin {name}, was kann ich für dich tun?',
    'onboarding.coordinator.home.greeting.2': 'Hi, ich bin {name}. Was wollen wir heute erkunden?',
    'onboarding.coordinator.home.greeting.3': '{name} hier — wie kann ich helfen?',
    'onboarding.coordinator.home.greeting.4': 'Ich bin {name}. Frag mich alles.',
    'onboarding.coordinator.home.greeting.5': 'Hey, {name} steht bereit. Was beschäftigt dich?',
    'onboarding.coordinator.home.greeting.6': 'Ich bin {name}. Legen wir los.',
    'onboarding.coordinator.home.selectorLabel': 'Start',
    'shell.newChat.subtitle': 'Eine neue Programmierkonversation beginnen',
  },
  'es-ES': {
    'onboarding.coordinator.home.greeting.1': 'Soy {name}, ¿qué puedo hacer por ti?',
    'onboarding.coordinator.home.greeting.2': 'Hola, soy {name}. ¿Qué exploramos hoy?',
    'onboarding.coordinator.home.greeting.3': '{name} aquí — ¿cómo puedo ayudar?',
    'onboarding.coordinator.home.greeting.4': 'Soy {name}. Pregúntame lo que quieras.',
    'onboarding.coordinator.home.greeting.5': 'Hola, {name} a tu servicio. ¿En qué piensas?',
    'onboarding.coordinator.home.greeting.6': 'Soy {name}. Empecemos.',
    'onboarding.coordinator.home.selectorLabel': 'Inicio',
    'shell.newChat.subtitle': 'Iniciar una nueva conversación de código',
  },
  'pt-BR': {
    'onboarding.coordinator.home.greeting.1': 'Sou {name}, o que posso fazer por você?',
    'onboarding.coordinator.home.greeting.2': 'Oi, sou {name}. O que vamos explorar hoje?',
    'onboarding.coordinator.home.greeting.3': '{name} aqui — como posso ajudar?',
    'onboarding.coordinator.home.greeting.4': 'Sou {name}. Pergunte-me qualquer coisa.',
    'onboarding.coordinator.home.greeting.5': 'Oi, {name} à disposição. O que você tem em mente?',
    'onboarding.coordinator.home.greeting.6': 'Sou {name}. Vamos começar.',
    'onboarding.coordinator.home.selectorLabel': 'Início',
    'shell.newChat.subtitle': 'Iniciar uma nova conversa de código',
  },
  'ru-RU': {
    'onboarding.coordinator.home.greeting.1': 'Я {name}, чем могу помочь?',
    'onboarding.coordinator.home.greeting.2': 'Привет, я {name}. Что исследуем сегодня?',
    'onboarding.coordinator.home.greeting.3': '{name} на связи — чем помочь?',
    'onboarding.coordinator.home.greeting.4': 'Я {name}. Спрашивайте что угодно.',
    'onboarding.coordinator.home.greeting.5': 'Привет, {name} к вашим услугам. О чём думаете?',
    'onboarding.coordinator.home.greeting.6': 'Я {name}. Давайте начнём.',
    'onboarding.coordinator.home.selectorLabel': 'Главная',
    'shell.newChat.subtitle': 'Начать новый разговор о коде',
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