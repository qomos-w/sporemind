/**
 * Quote-aware splitting of a debug console input line into args.
 *
 * Convention shared with the backend (workspace.debug_command_exec): the
 * frontend is responsible for tokenizing the raw line; the backend Dispatch
 * never re-parses. The tokenizer mirrors go-ebitor's console input splitter
 * (core_debug.go onSend): a double-quoted segment (with the quotes stripped)
 * or a run of non-whitespace characters.
 *
 * Examples:
 *   "help"                  -> ["help"]
 *   'echo "hello world"'    -> ["echo", "hello world"]
 *   'run --flag "a b" c'    -> ["run", "--flag", "a b", "c"]
 *   '""'                    -> [""]
 *   '   '                   -> []
 */
const DEBUG_INPUT_SPLIT_PATTERN = '"([^"]*)"|(\\S+)'

export function splitDebugInput(line: string): string[] {
  const re = new RegExp(DEBUG_INPUT_SPLIT_PATTERN, 'g')
  const args: string[] = []
  let m: RegExpExecArray | null
  while ((m = re.exec(line)) !== null) {
    if (m[1] !== undefined) {
      args.push(m[1])
    } else if (m[2] !== undefined) {
      args.push(m[2])
    }
  }
  return args
}
