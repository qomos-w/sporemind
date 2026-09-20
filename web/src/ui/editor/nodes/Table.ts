import type { PluginSimple } from "markdown-it";
import type { MarkdownSerializerState } from "prosemirror-markdown";
import type { Node as ProsemirrorNode, NodeSpec, Schema } from "prosemirror-model";
import { TextSelection } from "prosemirror-state";
import {
  addColumnAfter,
  addColumnBefore,
  addRowAfter,
  addRowBefore,
  deleteColumn,
  deleteRow,
  deleteTable,
} from "prosemirror-tables";
import { markdownItTable } from "markdown-it-table";
import Node from "../core/Node";
import type { CommandFactory, ExtensionTypeOptions } from "../core/types";

export default class Table extends Node {
  override get name(): string {
    return "table";
  }

  override get rulePlugins(): PluginSimple[] {
    return [markdownItTable as PluginSimple];
  }

  override get schema(): NodeSpec {
    return {
      content: "table_row+",
      tableRole: "table",
      isolating: true,
      group: "block",
      parseDOM: [{ tag: "table" }],
      toDOM: () => ["table", ["tbody", 0]],
    };
  }

  override toMarkdown(state: MarkdownSerializerState, node: ProsemirrorNode): void {
    let rowIndex = 0;
    node.forEach((row) => {
      state.write("|");
      row.forEach((cell) => {
        state.write(" ");
        renderCellContent(state, cell);
        state.write(" |");
      });
      state.write("\n");

      if (rowIndex === 0) {
        state.write("|");
        row.forEach(() => {
          state.write(" --- |");
        });
        state.write("\n");
      }
      rowIndex++;
    });
    state.closeBlock(node);
  }

  override parseMarkdown() {
    return { block: "table" } as const;
  }

  override commands(options: ExtensionTypeOptions) {
    const schema = options.schema;
    return {
      insert_table: insertTableCommand(schema),
      add_row_before: () => addRowBefore,
      add_row_after: () => addRowAfter,
      add_column_before: () => addColumnBefore,
      add_column_after: () => addColumnAfter,
      delete_row: () => deleteRow,
      delete_column: () => deleteColumn,
      delete_table: () => deleteTable,
    };
  }
}

function insertTableCommand(schema: Schema): CommandFactory {
  return (attrs = {}) => (state, dispatch) => {
    const rows = Math.max(1, Number(attrs.rows ?? 2));
    const cols = Math.max(1, Number(attrs.cols ?? 3));
    const withHeader = attrs.withHeader !== false;
    const table = createTableNode(schema, rows, cols, withHeader);
    const tr = state.tr.replaceSelectionWith(table);
    const tableStart = tr.selection.from;
    // Place cursor in the first cell's paragraph.
    const $cell = tr.doc.resolve(tableStart + 4);
    tr.setSelection(TextSelection.near($cell, 1));
    dispatch?.(tr.scrollIntoView());
    return true;
  };
}

function createTableNode(schema: Schema, rows: number, cols: number, withHeader: boolean): ProsemirrorNode {
  const { table, table_row, table_cell, table_header, paragraph } = schema.nodes;
  const rowNodes: ProsemirrorNode[] = [];
  for (let r = 0; r < rows; r++) {
    const cellNodes: ProsemirrorNode[] = [];
    for (let c = 0; c < cols; c++) {
      const cellType = withHeader && r === 0 ? table_header! : table_cell!;
      cellNodes.push(cellType.create(null, paragraph!.create()));
    }
    rowNodes.push(table_row!.create(null, cellNodes));
  }
  return table!.create(null, rowNodes);
}

/** Render the inline content of a cell's first paragraph (GFM cells are single-line). */
export function renderCellContent(state: MarkdownSerializerState, cell: ProsemirrorNode): void {
  const firstBlock = cell.firstChild;
  if (firstBlock && firstBlock.isTextblock) {
    state.renderInline(firstBlock);
  }
}
