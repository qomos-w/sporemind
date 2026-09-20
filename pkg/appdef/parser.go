package appdef

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/qomos-w/spore/schema"
	"github.com/qomos-w/spore/script"
)

// ParseFile parses a complete .appdef file content and returns the structured
// AST plus any diagnostics encountered during parsing.
func ParseFile(src string) (*AppDef, []Diagnostic, error) {
	var diags []Diagnostic

	// Stage 0: Capture contiguous `//` doc-comments above callable/bundle
	// block declarations from the ORIGINAL source. Must run before Stage 1
	// strips comments. Entries are keyed by document order (not line number)
	// because later schema extraction (struct/alias removal) shifts lines.
	commentDescs := extractCommentDescriptions(src)

	// Stage 1: Strip comments.
	cleaned := stripComments(src)

	// Stage 2: Locate and extract the app block.
	appName, appBody, appLine, err := extractAppBlock(cleaned)
	if err != nil {
		diags = append(diags, Diagnostic{Line: appLine, Message: err.Error()})
		return nil, diags, err
	}

	// Stage 3: Extract struct and type alias blocks and parse them via spore parser.
	schemaSrc, aliases, bodyAfterSchema, aliasDiags := extractSchemaBlocks(appBody, appLine)
	diags = append(diags, aliasDiags...)

	var structs []schema.ObjectDesc
	if strings.TrimSpace(schemaSrc) != "" {
		structs, err = script.ParseObjects(schemaSrc)
		if err != nil {
			return nil, diags, fmt.Errorf("struct parsing failed: %w", err)
		}
	}

	// Stage 4: Parse the remaining declaration blocks and kv pairs.
	scanner := newBodyScanner(bodyAfterSchema, appLine)
	meta, callables, entrypoints, events, listens, bundles, deps, freeAgent, pluginAgents, declDiags := scanner.parseDeclarations(appName, commentDescs)
	diags = append(diags, declDiags...)

	result := &AppDef{
		AppMeta:      meta,
		Structs:      structs,
		TypeAliases:  aliases,
		Callables:    callables,
		Entrypoints:  entrypoints,
		Events:       events,
		Listens:      listens,
		Bundles:      bundles,
		Dependencies: deps,
		FreeAgent:    freeAgent,
		PluginAgents: pluginAgents,
		SchemaSource: schemaSrc,
	}

	return result, diags, nil
}

// --- Comment Stripping ---

func stripComments(src string) string {
	var b strings.Builder
	i := 0
	for i < len(src) {
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '/' {
			for i < len(src) && src[i] != '\n' {
				i++
			}
			continue
		}
		if i+1 < len(src) && src[i] == '/' && src[i+1] == '*' {
			i += 2
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				i++
			}
			i += 2
			continue
		}
		b.WriteByte(src[i])
		i++
	}
	return b.String()
}

// --- Doc-Comment Descriptions ---

// commentDescs holds per-keyword doc-comment descriptions captured from the
// original source, in document order. take consumes the next entry for the
// keyword regardless of whether the corresponding block parses successfully —
// consumption must stay aligned with block DETECTION, not parse success, or
// later blocks would receive the previous block's comment.
type commentDescs struct {
	callable []string
	bundle   []string
	ci, bi   int
}

func (cd *commentDescs) take(keyword string) string {
	switch keyword {
	case "callable":
		if cd.ci < len(cd.callable) {
			s := cd.callable[cd.ci]
			cd.ci++
			return s
		}
	case "bundle":
		if cd.bi < len(cd.bundle) {
			s := cd.bundle[cd.bi]
			cd.bi++
			return s
		}
	}
	return ""
}

// extractCommentDescriptions scans the ORIGINAL .appdef source (before comment
// stripping) and returns, per keyword, the contiguous `//` comment block
// directly above each callable/bundle block-start line. A blank or non-comment
// line between the comment and the block detaches them (doc-comment
// adjacency convention). Multi-line comments join with a single space.
func extractCommentDescriptions(src string) *commentDescs {
	var out commentDescs
	lines := strings.Split(src, "\n")
	var pending []string

	flush := func(keyword string) {
		// Emit an entry for EVERY detected block — empty when no doc
		// comment is attached — so the per-keyword cursor counts BLOCKS,
		// matching take()'s per-detection consumption exactly. Emitting
		// only non-empty entries would shift later blocks' comments onto
		// their predecessors.
		desc := strings.TrimSpace(strings.Join(pending, " "))
		pending = pending[:0]
		switch keyword {
		case "callable":
			out.callable = append(out.callable, desc)
		case "bundle":
			out.bundle = append(out.bundle, desc)
		}
	}

	for _, raw := range lines {
		trimmed := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(trimmed, "//"):
			pending = append(pending, strings.TrimSpace(strings.TrimPrefix(trimmed, "//")))
		case trimmed == "":
			// A blank line inside or after a comment run detaches it from a
			// following block declaration.
			pending = pending[:0]
		case strings.HasPrefix(trimmed, "callable ") || strings.HasPrefix(trimmed, "callable\t") ||
			strings.HasPrefix(trimmed, "bundle ") || strings.HasPrefix(trimmed, "bundle\t"):
			keyword := "callable"
			if strings.HasPrefix(trimmed, "bundle") {
				keyword = "bundle"
			}
			flush(keyword)
		default:
			pending = pending[:0]
		}
	}
	return &out
}

// --- App Block Extraction ---

func extractAppBlock(src string) (name string, body string, line int, err error) {
	trimmed := strings.TrimSpace(src)
	if trimmed == "" {
		return "", "", 0, fmt.Errorf("empty .appdef file")
	}
	idx := findAppKeyword(trimmed)
	if idx < 0 {
		return "", "", 0, fmt.Errorf("missing top-level 'app' block declaration")
	}
	line = 1 + strings.Count(trimmed[:idx], "\n")
	rest := trimmed[idx:]
	rest = rest[len("app"):]
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)
	spaceIdx := strings.IndexAny(rest, " \t\n\r")
	if spaceIdx < 0 {
		return "", "", line, fmt.Errorf("expected app name after 'app' keyword")
	}
	name = rest[:spaceIdx]
	rest = strings.TrimLeftFunc(rest[spaceIdx:], unicode.IsSpace)
	if len(rest) == 0 || rest[0] != '{' {
		return "", "", line, fmt.Errorf("expected '{' after app name %q", name)
	}
	body, tail, err := extractBalancedBracesWithRest(rest)
	if err != nil {
		return "", "", line, fmt.Errorf("app block: %w", err)
	}
	if strings.TrimSpace(tail) != "" {
		tailLine := line + 1 + strings.Count(rest[:len(rest)-len(tail)], "\n")
		return "", "", tailLine, fmt.Errorf("content after app block (unbalanced braces? %d bytes starting %q) — declarations after this point were not parsed", len(tail), firstToken(tail))
	}
	return name, body, line, nil
}

func firstToken(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\r\n"); i > 0 {
		return s[:i]
	}
	if len(s) > 32 {
		return s[:32]
	}
	return s
}

func findAppKeyword(src string) int {
	lines := strings.Split(src, "\n")
	offset := 0
	for _, line := range lines {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		if strings.HasPrefix(trimmed, "app ") || strings.HasPrefix(trimmed, "app\t") {
			return offset + (len(line) - len(trimmed))
		}
		offset += len(line) + 1
	}
	return -1
}

// extractBalancedBracesWithRest additionally returns everything after the
// closing brace so callers can validate that a block ends where it should
// (an unbalanced stray '}' would otherwise silently truncate the parsed
// content — the admin-tools migration hit this as a "successful" parse of
// 9/28 callables plus misleading orphan-handler warnings).
func extractBalancedBracesWithRest(s string) (body string, rest string, err error) {
	if len(s) == 0 || s[0] != '{' {
		return "", "", fmt.Errorf("expected '{'")
	}
	depth := 0
	inString := false
	escape := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inString {
			escape = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
			if depth == 0 {
				return s[1:i], s[i+1:], nil
			}
		}
	}
	return "", "", fmt.Errorf("unterminated block (missing closing '}')")
}

func extractBalancedBraces(s string) (string, error) {
	body, _, err := extractBalancedBracesWithRest(s)
	return body, err
}

// --- Struct Block Extraction ---

func extractStructBlocks(body string, baseLine int) (structSrc string, remainder string) {
	var structs []string
	remainder = body

	for {
		idx := findKeywordAtLineStart(remainder, "struct")
		if idx < 0 {
			break
		}
		braceIdx := strings.Index(remainder[idx:], "{")
		if braceIdx < 0 {
			break
		}
		blockStart := idx + braceIdx
		block, err := extractBalancedBraces(remainder[blockStart:])
		if err != nil {
			break
		}
		fullEnd := blockStart + 1 + len(block) + 1
		fullBlock := remainder[idx:fullEnd]
		structs = append(structs, fullBlock)
		remainder = remainder[:idx] + remainder[fullEnd:]
	}

	structSrc = strings.Join(structs, "\n\n")
	return structSrc, remainder
}

// extractSchemaBlocks extracts both struct blocks and type alias lines from the
// app block body, returning a single schema source string (suitable for spore
// / gen-schema-ts), the parsed type aliases, and the remaining body text.
func extractSchemaBlocks(body string, baseLine int) (schemaSrc string, aliases []TypeAliasDecl, remainder string, diags []Diagnostic) {
	var parts []string

	remainder, aliases = extractTypeAliases(body, baseLine, &parts)
	structParts, remainder := extractStructBlocks(remainder, baseLine)

	if len(parts) > 0 && len(structParts) > 0 {
		parts = append(parts, "")
	}
	parts = append(parts, structParts)
	schemaSrc = strings.Join(parts, "\n\n")
	return schemaSrc, aliases, remainder, diags
}

// extractTypeAliases finds `type Alias = Target` declarations at line start and
// removes them from the body. Captured aliases are returned with absolute line
// numbers; the original source lines are also appended to parts so that the
// resulting schema source includes them for downstream codegen tools.
func extractTypeAliases(body string, baseLine int, parts *[]string) (remainder string, aliases []TypeAliasDecl) {
	lines := strings.Split(body, "\n")
	var remaining []string

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if m := typeAliasRe.FindStringSubmatch(trimmed); m != nil {
			aliases = append(aliases, TypeAliasDecl{
				Name:   m[1],
				Target: strings.TrimSpace(m[2]),
				Line:   baseLine + i + 1,
			})
			*parts = append(*parts, trimmed)
			continue
		}
		remaining = append(remaining, line)
	}

	return strings.Join(remaining, "\n"), aliases
}

func findKeywordAtLineStart(s, keyword string) int {
	lines := strings.Split(s, "\n")
	offset := 0
	for _, line := range lines {
		trimmed := strings.TrimLeftFunc(line, unicode.IsSpace)
		if strings.HasPrefix(trimmed, keyword+" ") || strings.HasPrefix(trimmed, keyword+"\t") || strings.HasPrefix(trimmed, keyword+"{") {
			return offset + (len(line) - len(trimmed))
		}
		offset += len(line) + 1
	}
	return -1
}

// --- Body Scanner ---

type bodyScanner struct {
	lines    []string
	pos      int
	baseLine int
}

func newBodyScanner(body string, baseLine int) *bodyScanner {
	return &bodyScanner{lines: strings.Split(body, "\n"), baseLine: baseLine}
}

func (bs *bodyScanner) currentLine() int { return bs.baseLine + bs.pos + 1 } // 1-based

func (bs *bodyScanner) skipBlankLines() {
	for bs.pos < len(bs.lines) && strings.TrimSpace(bs.lines[bs.pos]) == "" {
		bs.pos++
	}
}

func (bs *bodyScanner) parseDeclarations(appName string, comments *commentDescs) (
	AppMeta,
	[]CallableDecl,
	[]EntrypointDecl,
	[]EventDecl,
	[]ListenDecl,
	[]BundleDecl,
	[]DependencyDecl,
	*FreeAgentDecl,
	[]*PluginAgentDecl,
	[]Diagnostic,
) {
	var (
		meta         AppMeta
		callables    []CallableDecl
		entrypoints  []EntrypointDecl
		events       []EventDecl
		listens      []ListenDecl
		bundles      []BundleDecl
		deps         []DependencyDecl
		freeAgent    *FreeAgentDecl
		pluginAgents []*PluginAgentDecl
		diags        []Diagnostic
	)

	meta.Name = appName

	blockKeywords := map[string]bool{
		"callable": true, "entrypoint": true, "event": true,
		"listen": true, "bundle": true, "dependency": true,
		"free_agent": true, "plugin_agent": true,
	}

	for {
		bs.skipBlankLines()
		if bs.pos >= len(bs.lines) {
			break
		}

		line := strings.TrimSpace(bs.lines[bs.pos])
		if line == "" {
			bs.pos++
			continue
		}

		firstWord := getFirstWord(line)
		if blockKeywords[firstWord] {
			lineNum := bs.currentLine()
			// Consume the doc-comment entry at DETECTION time so the
			// per-keyword document-order cursor stays aligned even when the
			// block below fails to parse (empty/unclosed → early continue).
			var blockDesc string
			if comments != nil {
				blockDesc = comments.take(firstWord)
			}
			blockText, _ := bs.collectBlock()
			if blockText == "" {
				diags = append(diags, Diagnostic{Line: lineNum, Message: fmt.Sprintf("%s: failed to collect block text", firstWord)})
				continue
			}
			if !isBlockClosed(blockText) {
				diags = append(diags, Diagnostic{Line: lineNum, Message: fmt.Sprintf("%s: unterminated block (missing closing '}')", firstWord)})
				continue
			}

			switch firstWord {
			case "callable":
				cd, d := parseCallableBlock(blockText, lineNum)
				cd.Description = blockDesc
				diags = append(diags, d...)
				callables = append(callables, cd)
			case "entrypoint":
				ep, d := parseEntrypointBlock(blockText, lineNum)
				diags = append(diags, d...)
				entrypoints = append(entrypoints, ep)
			case "event":
				ed, d := parseEventBlock(blockText, lineNum)
				diags = append(diags, d...)
				events = append(events, ed)
			case "listen":
				ld, d := parseListenBlock(blockText, lineNum)
				diags = append(diags, d...)
				listens = append(listens, ld)
			case "bundle":
				bd, d := parseBundleBlock(blockText, lineNum)
				// description: KV stays authoritative; the doc-comment
				// fills in only when the KV is absent.
				if bd.Description == "" {
					bd.Description = blockDesc
				}
				diags = append(diags, d...)
				bundles = append(bundles, bd)
			case "dependency":
				dep, d := parseDependencyBlock(blockText, lineNum)
				diags = append(diags, d...)
				deps = append(deps, dep)
			case "free_agent":
				fa, d := parseFreeAgentBlock(blockText, lineNum)
				diags = append(diags, d...)
				if fa != nil {
					if freeAgent != nil {
						diags = append(diags, Diagnostic{Line: lineNum, Message: "free_agent: duplicate block (only one allowed)"})
					} else {
						freeAgent = fa
					}
				}
		case "plugin_agent":
			pa, d := parsePluginAgentBlock(blockText, lineNum)
			diags = append(diags, d...)
			if pa != nil {
				dup := false
				for _, prev := range pluginAgents {
					if prev.Name == pa.Name {
						dup = true
						break
					}
				}
				if dup {
					diags = append(diags, Diagnostic{Line: lineNum, Message: fmt.Sprintf("plugin_agent: duplicate slot %q", pa.Name)})
				} else {
					pluginAgents = append(pluginAgents, pa)
				}
			}
		}
		// bs.pos already advanced by collectBlock.
		continue
	}

	if isKVPair(line) {
		key, val := parseKVLine(line)
		applyMetaKV(&meta, key, val, bs.currentLine(), &diags)
		bs.pos++
		continue
	}

	diags = append(diags, Diagnostic{
		Line:    bs.currentLine(),
		Message: fmt.Sprintf("unexpected content: %q", truncate(line, 40)),
	})
	bs.pos++
	}

	return meta, callables, entrypoints, events, listens, bundles, deps, freeAgent, pluginAgents, diags
}

func (bs *bodyScanner) collectBlock() (string, int) {
	var parts []string
	startPos := bs.pos
	depth := 0
	started := false

	for bs.pos < len(bs.lines) {
		line := bs.lines[bs.pos]
		parts = append(parts, line)

		braceDelta := countBraceDelta(line)
		depth += braceDelta

		// Mark that we've entered a block if this line contains '{'.
		if !started && strings.Contains(line, "{") {
			started = true
		}

		// For a single-line block like `callable foo { ... }`, the depth delta
		// is 0. We still need to break.
		if started && depth <= 0 {
			bs.pos++
			break
		}

		bs.pos++
	}

	return strings.Join(parts, "\n"), bs.pos - startPos
}

func countBraceDelta(line string) int {
	depth := 0
	inString := false
	escape := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inString {
			escape = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			depth++
		} else if ch == '}' {
			depth--
		}
	}
	return depth
}

func getFirstWord(s string) string {
	s = strings.TrimSpace(s)
	for i, ch := range s {
		if ch == ' ' || ch == '\t' {
			return s[:i]
		}
	}
	return s
}

func isKVPair(line string) bool {
	colonIdx := strings.Index(line, ":")
	if colonIdx <= 0 {
		return false
	}
	key := strings.TrimSpace(line[:colonIdx])
	if key == "" {
		return false
	}
	for _, ch := range key {
		if !isValidIdentChar(ch) {
			return false
		}
	}
	return true
}

func isValidIdentChar(ch rune) bool {
	return ch == '_' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
}

func parseKVLine(line string) (key, val string) {
	colonIdx := strings.Index(line, ":")
	key = strings.TrimSpace(line[:colonIdx])
	val = strings.TrimSpace(line[colonIdx+1:])
	return
}

func applyMetaKV(meta *AppMeta, key, val string, line int, diags *[]Diagnostic) {
	switch strings.ToLower(key) {
	case "id":
		meta.ID = unquote(val)
	case "name":
		meta.Name = unquote(val)
	case "version":
		meta.Version = unquote(val)
	case "namespace":
		meta.Namespace = unquote(val)
	case "permissions":
		perms, err := parseStringArray(val)
		if err != nil {
			*diags = append(*diags, Diagnostic{Line: line, Message: fmt.Sprintf("permissions: %v", err)})
		} else {
			meta.Permissions = perms
		}
	}
}

// --- Block Parsers ---

func parseCallableBlock(text string, lineNum int) (CallableDecl, []Diagnostic) {
	var cd CallableDecl
	cd.Line = lineNum

	rest := strings.TrimSpace(text)
	rest = rest[len("callable"):]
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)

	idEnd := strings.IndexAny(rest, " \t\n{")
	if idEnd <= 0 {
		return cd, []Diagnostic{{Line: lineNum, Message: "callable: expected id and '{'"}}
	}
	cd.ID = rest[:idEnd]

	braceIdx := strings.Index(rest, "{")
	if braceIdx < 0 {
		return cd, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("callable %q: expected '{'", cd.ID)}}
	}
	body, err := extractBalancedBraces(rest[braceIdx:])
	if err != nil {
		return cd, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("callable %q: %v", cd.ID, err)}}
	}

	for k, v := range parseBlockKV(body) {
		switch strings.ToLower(k) {
		case "request":
			cd.Request = v
		case "response":
			cd.Response = v
		case "effect":
			cd.Effect = unquote(v)
		case "toolname":
			cd.ToolName = unquote(v)
		case "expose":
			cd.Expose = unquote(v)
		case "watch":
			watch, err := parseStringArray(v)
			if err != nil {
				return cd, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("callable %q: invalid watch list: %v", cd.ID, err)}}
			}
			cd.Watch = watch
		case "service":
			cd.Service = unquote(v)
		case "streaming":
			cd.Streaming = strings.ToLower(v) == "true"
		case "timeout":
			d, err := time.ParseDuration(unquote(v))
			if err != nil {
				return cd, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("callable %q: invalid timeout %q: %v", cd.ID, v, err)}}
			}
			cd.TimeoutMs = d.Milliseconds()
		case "timeout_ms":
			ms, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return cd, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("callable %q: invalid timeout_ms %q: %v", cd.ID, v, err)}}
			}
			cd.TimeoutMs = ms
		}
	}

	return cd, nil
}

func parseEntrypointBlock(text string, lineNum int) (EntrypointDecl, []Diagnostic) {
	var ep EntrypointDecl
	ep.Line = lineNum

	rest := strings.TrimSpace(text)
	rest = rest[len("entrypoint"):]
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)

	parts := strings.Fields(rest)
	if len(parts) < 2 {
		return ep, []Diagnostic{{Line: lineNum, Message: "entrypoint: expected 'kind id { ... }'"}}
	}
	ep.Kind = parts[0]

	rest = strings.TrimLeftFunc(rest[len(parts[0]):], unicode.IsSpace)
	idEnd := strings.IndexAny(rest, " \t\n{")
	if idEnd <= 0 {
		return ep, []Diagnostic{{Line: lineNum, Message: "entrypoint: expected id and '{'"}}
	}
	ep.ID = rest[:idEnd]

	braceIdx := strings.Index(rest, "{")
	if braceIdx < 0 {
		return ep, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("entrypoint %q: expected '{'", ep.ID)}}
	}
	body, err := extractBalancedBraces(rest[braceIdx:])
	if err != nil {
		return ep, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("entrypoint %q: %v", ep.ID, err)}}
	}

	for k, v := range parseBlockKV(body) {
		switch strings.ToLower(k) {
		case "title":
			ep.Title = unquote(v)
		case "route":
			ep.Route = unquote(v)
		}
	}

	return ep, nil
}

func parseEventBlock(text string, lineNum int) (EventDecl, []Diagnostic) {
	var ed EventDecl
	ed.Line = lineNum

	rest := strings.TrimSpace(text)
	rest = rest[len("event"):]
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)

	idEnd := strings.IndexAny(rest, " \t\n{")
	if idEnd <= 0 {
		return ed, []Diagnostic{{Line: lineNum, Message: "event: expected id and '{'"}}
	}
	ed.ID = rest[:idEnd]

	braceIdx := strings.Index(rest, "{")
	if braceIdx < 0 {
		return ed, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("event %q: expected '{'", ed.ID)}}
	}
	body, err := extractBalancedBraces(rest[braceIdx:])
	if err != nil {
		return ed, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("event %q: %v", ed.ID, err)}}
	}

	for k, v := range parseBlockKV(body) {
		switch strings.ToLower(k) {
		case "payload":
			ed.Payload = v
		case "permission":
			ed.Permission = unquote(v)
		}
	}

	return ed, nil
}

// parseListenBlock parses `listen <kind> { }` — an inbound host event
// subscription. The body is intentionally empty in phase 1 (kind→payload
// typing comes from the appbinding EventCatalog, not from appdef structs);
// unknown fields are rejected so future filters (e.g. app+event scoping for
// app_event) can be added without silent no-ops.
func parseListenBlock(text string, lineNum int) (ListenDecl, []Diagnostic) {
	var ld ListenDecl
	ld.Line = lineNum

	rest := strings.TrimSpace(text)
	rest = rest[len("listen"):]
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)

	kindEnd := strings.IndexAny(rest, " \t\n{")
	if kindEnd <= 0 {
		return ld, []Diagnostic{{Line: lineNum, Message: "listen: expected event kind and '{' (e.g. listen app_lifecycle { })"}}
	}
	ld.Kind = rest[:kindEnd]

	braceIdx := strings.Index(rest, "{")
	if braceIdx < 0 {
		return ld, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("listen %q: expected '{'", ld.Kind)}}
	}
	body, err := extractBalancedBraces(rest[braceIdx:])
	if err != nil {
		return ld, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("listen %q: %v", ld.Kind, err)}}
	}
	for k := range parseBlockKV(body) {
		return ld, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("listen %q: unexpected field %q — phase 1 takes no fields; filters arrive with per-event scoping", ld.Kind, k)}}
	}

	return ld, nil
}

func parseBundleBlock(text string, lineNum int) (BundleDecl, []Diagnostic) {
	var bd BundleDecl
	bd.Line = lineNum

	rest := strings.TrimSpace(text)
	rest = rest[len("bundle"):]
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)

	braceIdx := strings.Index(rest, "{")
	if braceIdx < 0 {
		return bd, []Diagnostic{{Line: lineNum, Message: "bundle: expected '{'"}}
	}

	body, err := extractBalancedBraces(rest[braceIdx:])
	if err != nil {
		return bd, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("bundle: %v", err)}}
	}

	for k, v := range parseBlockKV(body) {
		switch strings.ToLower(k) {
		case "title":
			bd.Title = unquote(v)
		case "description":
			bd.Description = unescapeNewlines(unquote(v))
		case "icon":
			bd.Icon = unquote(v)
		case "color":
			bd.Color = unquote(v)
		case "tools":
			tools, err := parseIdentArray(v)
			if err != nil {
				return bd, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("bundle tools: %v", err)}}
			}
			bd.Tools = tools
		}
	}

	return bd, nil
}

// parseFreeAgentBlock parses a free_agent { ... } block. Flat KV keys:
// allow_create, allow_switch, allow_message, agent_kinds.
func parseFreeAgentBlock(text string, lineNum int) (*FreeAgentDecl, []Diagnostic) {
	rest := strings.TrimSpace(text)
	rest = rest[len("free_agent"):]
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)

	braceIdx := strings.Index(rest, "{")
	if braceIdx < 0 {
		return nil, []Diagnostic{{Line: lineNum, Message: "free_agent: expected '{'"}}
	}
	body, err := extractBalancedBraces(rest[braceIdx:])
	if err != nil {
		return nil, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("free_agent: %v", err)}}
	}

	decl := &FreeAgentDecl{Line: lineNum}
	for k, v := range parseBlockKV(body) {
		switch strings.ToLower(k) {
		case "allow_create":
			decl.AllowCreate = strings.EqualFold(v, "true")
		case "allow_switch":
			decl.AllowSwitch = strings.EqualFold(v, "true")
		case "allow_message":
			decl.AllowMessage = strings.EqualFold(v, "true")
		case "agent_kinds":
			kinds, err := parseIdentArray(v)
			if err != nil {
				return decl, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("free_agent agent_kinds: %v", err)}}
			}
			decl.AgentKinds = kinds
		}
	}
	return decl, nil
}

// parsePluginAgentBlock parses a plugin_agent [name] { ... } block. Flat KV
// keys: display_name, system_prompt, bundles, model. Values follow the same
// quoting conventions as bundle blocks (double-quoted strings, \n escapes).
// The optional name header between the keyword and '{' is the binding slot;
// a block without a header binds the "default" slot.
func parsePluginAgentBlock(text string, lineNum int) (*PluginAgentDecl, []Diagnostic) {
	rest := strings.TrimSpace(text)
	rest = rest[len("plugin_agent"):]
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)

	braceIdx := strings.Index(rest, "{")
	if braceIdx < 0 {
		return nil, []Diagnostic{{Line: lineNum, Message: "plugin_agent: expected '{'"}}
	}
	body, err := extractBalancedBraces(rest[braceIdx:])
	if err != nil {
		return nil, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("plugin_agent: %v", err)}}
	}

	name := strings.TrimSpace(rest[:braceIdx])
	if name == "" {
		name = "default"
	}
	decl := &PluginAgentDecl{Name: name, Line: lineNum}
	for k, v := range parseBlockKV(body) {
		switch strings.ToLower(k) {
		case "display_name":
			decl.DisplayName = unquote(v)
		case "system_prompt":
			decl.SystemPrompt = unescapeNewlines(unquote(v))
		case "bundles":
			ids, err := parseStringArray(v)
			if err != nil {
				return decl, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("plugin_agent bundles: %v", err)}}
			}
			decl.Bundles = ids
		case "model":
			decl.Model = unquote(v)
		}
	}
	return decl, nil
}

func parseDependencyBlock(text string, lineNum int) (DependencyDecl, []Diagnostic) {
	var dep DependencyDecl
	dep.Line = lineNum

	rest := strings.TrimSpace(text)
	rest = rest[len("dependency"):]
	rest = strings.TrimLeftFunc(rest, unicode.IsSpace)

	braceIdx := strings.Index(rest, "{")
	if braceIdx < 0 {
		return dep, []Diagnostic{{Line: lineNum, Message: "dependency: expected '{'"}}
	}

	prefix := strings.TrimSpace(rest[:braceIdx])
	if prefix != "" {
		dep.ID = prefix
	}

	body, err := extractBalancedBraces(rest[braceIdx:])
	if err != nil {
		return dep, []Diagnostic{{Line: lineNum, Message: fmt.Sprintf("dependency: %v", err)}}
	}

	for k, v := range parseBlockKV(body) {
		switch strings.ToLower(k) {
		case "id":
			if dep.ID == "" {
				dep.ID = unquote(v)
			}
		case "version":
			dep.Version = unquote(v)
		}
	}

	return dep, nil
}

// isBlockClosed reports whether the collected block text contains a matching
// closing brace for the first opening brace. It does a simple brace balance
// check, ignoring string contents.
func isBlockClosed(text string) bool {
	depth := 0
	inString := false
	escape := false
	started := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inString {
			escape = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '{' {
			started = true
			depth++
		} else if ch == '}' {
			depth--
			if started && depth <= 0 {
				return true
			}
		}
	}
	return false
}

// --- KV Parsing Helpers ---

func parseBlockKV(body string) map[string]string {
	result := make(map[string]string)
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		colonIdx := strings.Index(line, ":")
		if colonIdx <= 0 {
			continue
		}
		key := strings.TrimSpace(line[:colonIdx])
		val := strings.TrimSpace(line[colonIdx+1:])
		if key != "" {
			result[key] = val
		}
	}
	return result
}

func parseStringArray(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, fmt.Errorf("expected array literal, got %q", truncate(s, 30))
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return nil, nil
	}
	parts := splitArrayElements(inner)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		result = append(result, unquote(p))
	}
	return result, nil
}

func parseIdentArray(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if len(s) < 2 || s[0] != '[' || s[len(s)-1] != ']' {
		return nil, fmt.Errorf("expected array literal, got %q", truncate(s, 30))
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	if inner == "" {
		return nil, nil
	}
	parts := splitArrayElements(inner)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result, nil
}

func splitArrayElements(s string) []string {
	var parts []string
	depth := 0
	inString := false
	escape := false
	start := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if escape {
			escape = false
			continue
		}
		if ch == '\\' && inString {
			escape = true
			continue
		}
		if ch == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if ch == '<' || ch == '[' || ch == '(' {
			depth++
		} else if ch == '>' || ch == ']' || ch == ')' {
			depth--
		} else if ch == ',' && depth == 0 {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	return s
}

// unescapeNewlines turns "\n" escapes into real newlines. A bundle
// description is a single-line value in .appdef but is injected verbatim into
// the mounting agent's prompt as markdown, so authors write \n (and \n\n for
// paragraph breaks) and the parser unwraps them here. Only bundle
// descriptions get this treatment; other quoted fields stay literal.
func unescapeNewlines(s string) string {
	if !strings.Contains(s, `\n`) {
		return s
	}
	return strings.NewReplacer(`\\`, `\`, `\n`, "\n").Replace(s)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
