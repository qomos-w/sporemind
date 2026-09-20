import { savePreference, loadPreference } from './theme-persist'
import { parseKeymapState, defaultKeymapState, type KeymapStateV2 } from '../ui/editor/keymap-schemes'

const KEYMAP_PREFERENCE_KEY = 'editor.keymap.v1'

let writeVersion = 0

export async function persistEditorKeymap(state: KeymapStateV2): Promise<void> {
  await savePreference(
    KEYMAP_PREFERENCE_KEY,
    JSON.stringify(state),
    'editor-keymap',
    { v: writeVersion },
  )
}

export async function loadEditorKeymap(): Promise<KeymapStateV2> {
  const raw = await loadPreference(KEYMAP_PREFERENCE_KEY)
  return parseKeymapState(raw)
}

export { defaultKeymapState }
