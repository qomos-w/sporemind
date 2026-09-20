import type { Transaction } from "prosemirror-state";
import type { NodeType, Attrs } from "prosemirror-model";
import { setBlockType, lift } from "prosemirror-commands";
import { findWrapping } from "prosemirror-transform";
import type { CommandFactory } from "./types";

type Dispatch = (tr: Transaction) => void | undefined;

/**
 * Toggle the current textblock to `type` (with optional attrs), or back to a
 * paragraph when already active. Returns a CommandFactory for use in toolbars/menus.
 */
export function toggleBlockType(
  type: NodeType,
  paragraphType: NodeType,
  attrs: Attrs = {},
): CommandFactory {
  return () => (state, dispatch) => {
    const { $from } = state.selection;
    const isActive = $from.parent.hasMarkup(type, attrs);
    return setBlockType(isActive ? paragraphType : type, attrs)(state, dispatch as Dispatch);
  };
}

/** Wrap the selection in the given block type, or lift (unwrap) when already inside one. */
export function toggleWrap(type: NodeType): CommandFactory {
  return () => (state, dispatch) => {
    const { $from, $to } = state.selection;
    const range = $from.blockRange($to);
    if (!range) return false;
    const isActive = range.parent.type === type;
    if (isActive) {
      return lift(state, dispatch as Dispatch);
    }
    const wrapping = findWrapping(range, type);
    if (!wrapping) return false;
    const tr = state.tr.wrap(range, wrapping).scrollIntoView();
    dispatch?.(tr);
    return true;
  };
}

