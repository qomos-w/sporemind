import { Plugin } from "prosemirror-state";
import type { MarkdownParser } from "prosemirror-markdown";

/**
 * Heuristic: score how "markdown-like" a text string is.
 * Returns true if the text likely contains markdown formatting that should be
 * parsed into rich content rather than inserted as plain text.
 */
export function isMarkdown(text: string): boolean {
  const lines = text.split("\n");
  const lineCount = lines.length;
  let score = 0;

  for (const line of lines) {
    if (/^#{1,6}\s/.test(line)) score++;        // heading
    if (/^[-+*]\s/.test(line)) score++;          // bullet list
    if (/^\d+\.\s/.test(line)) score++;          // ordered list
    if (/^>\s/.test(line)) score++;              // blockquote
    if (/^```/.test(line)) score++;              // code fence
    if (/^\|.*\|/.test(line)) score++;           // table row
    if (/^---+$/.test(line.trim())) score++;     // horizontal rule
    if (/^- \[[ xX]\]\s/.test(line)) score++;    // task list
  }

  // Inline signals
  if (/\*\*[^*]+\*\*/.test(text)) score++;       // bold
  if (/__[^_]+__/.test(text)) score++;           // bold alt
  if (/(?<!\*)\*[^*]+\*(?!\*)/.test(text)) score++; // italic
  if (/`[^`]+`/.test(text)) score++;             // inline code
  if (/\[.+?\]\(.+?\)/.test(text)) score++;      // link
  if (/~~[^~]+~~/.test(text)) score++;           // strikethrough
  if (/\[\[.+?\]\]/.test(text)) score++;         // wikiword

  // Multi-line content with any signal is likely markdown.
  // Single-line content needs stronger signals (2+).
  const threshold = lineCount > 1 ? 1 : 2;
  return score >= threshold;
}

/**
 * Create a ProseMirror plugin that intercepts paste events and parses
 * markdown content into rich document nodes — the "instant rendering"
 * feature from Outline.
 *
 * Behavior:
 * - If pasted text is detected as markdown → parse and insert as ProseMirror slice.
 * - If a URL is pasted over a text selection → wrap selection in a link mark.
 * - Otherwise → fall through to ProseMirror's default paste handling.
 *
 * Holding Shift while pasting forces markdown parsing regardless of detection.
 */
export function markdownPastePlugin(parser: MarkdownParser): Plugin {
  return new Plugin({
    props: {
      handlePaste: (view, event) => {
        const clipboard = event.clipboardData;
        if (!clipboard) return false;

        const state = view.state;

        // Inside a code block, always paste as plain text.
        if (state.selection.$from.parent.type.spec.code) return false;

        const text = clipboard.getData("text/plain");
        if (!text) return false;

        const forceMarkdown = (event as ClipboardEvent & { shiftKey?: boolean }).shiftKey;

        // URL-on-selection: paste a single URL over selected text → create link.
        const urlMatch = text.trim().match(/^https?:\/\/\S+$/);
        if (urlMatch && !urlMatch[0].includes("\n") && !state.selection.empty) {
          const markType = state.schema.marks["link"];
          if (markType) {
            const tr = state.tr.addMark(
              state.selection.from,
              state.selection.to,
              markType.create({ href: urlMatch[0] }),
            );
            view.dispatch(tr.scrollIntoView());
            return true;
          }
        }

        // Only parse as markdown if the text looks like markdown (or Shift is held).
        if (!forceMarkdown && !isMarkdown(text)) return false;

        let parsed;
        try {
          parsed = parser.parse(text);
        } catch {
          return false;
        }
        if (!parsed) return false;

        // Extract the doc's content as a slice for insertion.
        const slice = parsed.slice(0);
        const tr = state.tr.replaceSelection(slice);
        tr.scrollIntoView();

        if (tr.docChanged) {
          view.dispatch(tr);
          return true;
        }

        return false;
      },
    },
  });
}
