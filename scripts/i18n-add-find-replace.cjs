// One-off: inject the local find/replace panel i18n keys (findReplace.*)
// into all 10 locale files, extending the namespace introduced with the
// global find/replace overlay keys. Idempotent: existing keys are kept.
//
// Run: node scripts/i18n-add-find-replace.cjs
// Then verify: cd web && npm run i18n:check
const fs = require('fs')
const path = require('path')

const dir = path.join(__dirname, '..', 'web', 'src', 'i18n', 'locales')

// Local (in-editor) FindReplacePanel strings. The global overlay's keys
// (titleFind/titleReplace/noResults/applySummary/...) already exist and are
// reused by the panel, so only these local-specific keys are injected.
const en = {
  'findReplace.find': 'Find',
  'findReplace.replaceWith': 'Replace with',
  'findReplace.matchCase': 'Match case',
  'findReplace.wholeWords': 'Whole words',
  'findReplace.regex': 'Regular expression',
  'findReplace.searchOptions': 'Search options',
  'findReplace.previousMatch': 'Previous match (Shift+Enter)',
  'findReplace.nextMatch': 'Next match (Enter)',
  'findReplace.replace': 'Replace',
  'findReplace.replaceAllOccurrences': 'Replace All',
  'findReplace.closePanel': 'Close (Esc)',
  'findReplace.closeFindPanel': 'Close find panel',
}

const locales = {
  'en-US': en,
  'zh-CN': {
    'findReplace.find': '查找',
    'findReplace.replaceWith': '替换为',
    'findReplace.matchCase': '区分大小写',
    'findReplace.wholeWords': '全词匹配',
    'findReplace.regex': '正则表达式',
    'findReplace.searchOptions': '搜索选项',
    'findReplace.previousMatch': '上一个匹配（Shift+Enter）',
    'findReplace.nextMatch': '下一个匹配（Enter）',
    'findReplace.replace': '替换',
    'findReplace.replaceAllOccurrences': '全部替换',
    'findReplace.closePanel': '关闭（Esc）',
    'findReplace.closeFindPanel': '关闭查找面板',
  },
  'zh-TW': {
    'findReplace.find': '尋找',
    'findReplace.replaceWith': '取代為',
    'findReplace.matchCase': '區分大小寫',
    'findReplace.wholeWords': '全字比對',
    'findReplace.regex': '正規表示式',
    'findReplace.searchOptions': '搜尋選項',
    'findReplace.previousMatch': '上一個比對（Shift+Enter）',
    'findReplace.nextMatch': '下一個比對（Enter）',
    'findReplace.replace': '取代',
    'findReplace.replaceAllOccurrences': '全部取代',
    'findReplace.closePanel': '關閉（Esc）',
    'findReplace.closeFindPanel': '關閉尋找面板',
  },
  'ja-JP': {
    'findReplace.find': '検索',
    'findReplace.replaceWith': '置換後',
    'findReplace.matchCase': '大文字と小文字を区別',
    'findReplace.wholeWords': '単語単位で一致',
    'findReplace.regex': '正規表現',
    'findReplace.searchOptions': '検索オプション',
    'findReplace.previousMatch': '前を検索（Shift+Enter）',
    'findReplace.nextMatch': '次を検索（Enter）',
    'findReplace.replace': '置換',
    'findReplace.replaceAllOccurrences': 'すべて置換',
    'findReplace.closePanel': '閉じる（Esc）',
    'findReplace.closeFindPanel': '検索パネルを閉じる',
  },
  'ko-KR': {
    'findReplace.find': '찾기',
    'findReplace.replaceWith': '바꿀 내용',
    'findReplace.matchCase': '대소문자 구분',
    'findReplace.wholeWords': '단어 단위',
    'findReplace.regex': '정규식',
    'findReplace.searchOptions': '검색 옵션',
    'findReplace.previousMatch': '이전 일치 (Shift+Enter)',
    'findReplace.nextMatch': '다음 일치 (Enter)',
    'findReplace.replace': '바꾸기',
    'findReplace.replaceAllOccurrences': '모두 바꾸기',
    'findReplace.closePanel': '닫기 (Esc)',
    'findReplace.closeFindPanel': '찾기 패널 닫기',
  },
  'fr-FR': {
    'findReplace.find': 'Rechercher',
    'findReplace.replaceWith': 'Remplacer par',
    'findReplace.matchCase': 'Respecter la casse',
    'findReplace.wholeWords': 'Mot entier',
    'findReplace.regex': 'Expression régulière',
    'findReplace.searchOptions': 'Options de recherche',
    'findReplace.previousMatch': 'Occurrence précédente (Maj+Entrée)',
    'findReplace.nextMatch': 'Occurrence suivante (Entrée)',
    'findReplace.replace': 'Remplacer',
    'findReplace.replaceAllOccurrences': 'Tout remplacer',
    'findReplace.closePanel': 'Fermer (Échap)',
    'findReplace.closeFindPanel': 'Fermer le panneau de recherche',
  },
  'de-DE': {
    'findReplace.find': 'Suchen',
    'findReplace.replaceWith': 'Ersetzen mit',
    'findReplace.matchCase': 'Groß-/Kleinschreibung beachten',
    'findReplace.wholeWords': 'Ganzes Wort',
    'findReplace.regex': 'Regulärer Ausdruck',
    'findReplace.searchOptions': 'Suchoptionen',
    'findReplace.previousMatch': 'Vorheriger Treffer (Umschalt+Eingabe)',
    'findReplace.nextMatch': 'Nächster Treffer (Eingabe)',
    'findReplace.replace': 'Ersetzen',
    'findReplace.replaceAllOccurrences': 'Alle ersetzen',
    'findReplace.closePanel': 'Schließen (Esc)',
    'findReplace.closeFindPanel': 'Suchfeld schließen',
  },
  'es-ES': {
    'findReplace.find': 'Buscar',
    'findReplace.replaceWith': 'Reemplazar por',
    'findReplace.matchCase': 'Distinguir mayúsculas',
    'findReplace.wholeWords': 'Palabra completa',
    'findReplace.regex': 'Expresión regular',
    'findReplace.searchOptions': 'Opciones de búsqueda',
    'findReplace.previousMatch': 'Coincidencia anterior (Mayús+Intro)',
    'findReplace.nextMatch': 'Coincidencia siguiente (Intro)',
    'findReplace.replace': 'Reemplazar',
    'findReplace.replaceAllOccurrences': 'Reemplazar todo',
    'findReplace.closePanel': 'Cerrar (Esc)',
    'findReplace.closeFindPanel': 'Cerrar panel de búsqueda',
  },
  'pt-BR': {
    'findReplace.find': 'Buscar',
    'findReplace.replaceWith': 'Substituir por',
    'findReplace.matchCase': 'Diferenciar maiúsculas',
    'findReplace.wholeWords': 'Palavra inteira',
    'findReplace.regex': 'Expressão regular',
    'findReplace.searchOptions': 'Opções de pesquisa',
    'findReplace.previousMatch': 'Ocorrência anterior (Shift+Enter)',
    'findReplace.nextMatch': 'Próxima ocorrência (Enter)',
    'findReplace.replace': 'Substituir',
    'findReplace.replaceAllOccurrences': 'Substituir tudo',
    'findReplace.closePanel': 'Fechar (Esc)',
    'findReplace.closeFindPanel': 'Fechar painel de pesquisa',
  },
  'ru-RU': {
    'findReplace.find': 'Поиск',
    'findReplace.replaceWith': 'Заменить на',
    'findReplace.matchCase': 'Учитывать регистр',
    'findReplace.wholeWords': 'Только целое слово',
    'findReplace.regex': 'Регулярное выражение',
    'findReplace.searchOptions': 'Параметры поиска',
    'findReplace.previousMatch': 'Предыдущее совпадение (Shift+Enter)',
    'findReplace.nextMatch': 'Следующее совпадение (Enter)',
    'findReplace.replace': 'Заменить',
    'findReplace.replaceAllOccurrences': 'Заменить всё',
    'findReplace.closePanel': 'Закрыть (Esc)',
    'findReplace.closeFindPanel': 'Закрыть панель поиска',
  },
}

/** Locale JSON files are 2-space indented, CRLF, without trailing newline. */
function serialize(data) {
  return JSON.stringify(data, null, 2).replace(/\n/g, '\r\n')
}

function injectKeys(file, additions) {
  const raw = fs.readFileSync(file, 'utf8')
  const data = JSON.parse(raw)
  // Format guard: refuse to run if the file's current serialization differs,
  // so the script can never cause an unrelated full-file reformat.
  if (serialize(data) !== raw) {
    throw new Error(`${path.basename(file)}: serialization round-trip mismatch, aborting`)
  }

  const out = {}
  let inserted = false
  let added = 0
  let skipped = 0
  for (const [key, value] of Object.entries(data)) {
    out[key] = value
    // Insert the new keys right after the last existing findReplace.* key so
    // the whole feature namespace stays grouped together.
    if (!inserted && key.startsWith('findReplace.')) {
      let last = key
      for (const k of Object.keys(data)) if (k.startsWith('findReplace.')) last = k
      if (key === last) {
        for (const [nk, nv] of Object.entries(additions)) {
          if (nk in data) { skipped++; continue }
          out[nk] = nv
          added++
        }
        inserted = true
      }
    }
  }
  if (!inserted) throw new Error(`${path.basename(file)}: no findReplace.* anchor key found`)
  if (added) fs.writeFileSync(file, serialize(out), 'utf8')
  return { added, skipped }
}

let totalAdded = 0
for (const [locale, additions] of Object.entries(locales)) {
  const file = path.join(dir, `${locale}.json`)
  const { added, skipped } = injectKeys(file, additions)
  totalAdded += added
  console.log(`${locale}: +${added} added, ${skipped} already present`)
}
console.log(`Done: ${totalAdded} keys injected across ${Object.keys(locales).length} locales.`)
