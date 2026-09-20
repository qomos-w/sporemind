import { InputRule } from "prosemirror-inputrules";
import type { MarkType } from "prosemirror-model";

/**
 * Create an input rule that wraps typed text in a mark when the user types the
 * closing delimiter — e.g. typing `**bold**` applies the bold mark and removes
 * the delimiters. `pattern` is the delimiter string (e.g. "**", "`", "~~").
 */
export function markInputRule(pattern: string, markType: MarkType): InputRule {
  const char = pattern[0];
  const esc = pattern.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  // Matches: delimiter + non-space-start ... non-space-end + delimiter, at end of input.
  const regex = new RegExp(`(${esc})([^${char}\\s]|[^${char}\\s].*?[^${char}\\s])(${esc})$`);

  return new InputRule(regex, (state, match, start, end) => {
    const [full, , text] = match;
    if (!full || text === undefined) return null;

    const tr = state.tr;
    const delimsLen = pattern.length;
    // Keep only the inner text; drop the surrounding delimiters.
    tr.delete(start + delimsLen, end - delimsLen + full.length);
    // After delete the offsets shift; recompute against the stable `start`.
    tr.addMark(start + delimsLen, start + delimsLen + text.length, markType.create());
    // Remove the now-detached delimiters at the edges.
    tr.delete(start, start + delimsLen);
    tr.delete(start + text.length, start + text.length + delimsLen);
    tr.removeStoredMark(markType);
    return tr;
  });
}
