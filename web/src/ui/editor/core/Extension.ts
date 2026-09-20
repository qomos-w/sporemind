import type { InputRule } from "prosemirror-inputrules";
import type { PluginSimple } from "markdown-it";
import type { Command, Plugin } from "prosemirror-state";
import type { CommandFactory, ExtensionTypeOptions } from "./types";

export default abstract class Extension<TOptions extends object = object> {
  options: TOptions;

  constructor(options: Partial<TOptions> = {}) {
    this.options = { ...this.defaultOptions, ...options } as TOptions;
  }

  get type(): "extension" | "node" | "mark" {
    return "extension";
  }

  get name(): string {
    return "";
  }

  get defaultOptions(): Partial<TOptions> {
    return {};
  }

  /**
   * markdown-it plugins that register custom parsing rules (e.g. a non-CommonMark
   * inline syntax like `~~strike~~` or future `[[wikiword]]`). Collected by the
   * ExtensionManager and applied to the parser's tokenizer.
   */
  get rulePlugins(): PluginSimple[] {
    return [];
  }

  /** ProseMirror plugins contributed by this extension (e.g. history, menus). */
  get plugins(): Plugin[] {
    return [];
  }

  keys(_options: ExtensionTypeOptions): Record<string, Command> {
    return {};
  }

  inputRules(_options: ExtensionTypeOptions): InputRule[] {
    return [];
  }

  commands(
    _options: ExtensionTypeOptions,
  ): Record<string, CommandFactory> | CommandFactory | undefined {
    return undefined;
  }
}
