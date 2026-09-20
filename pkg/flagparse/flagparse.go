// Package flagparse parses POSIX-style command-line strings into Go structs
// using struct field tags. It is designed for LLM-facing tools that accept
// shell-style command strings.
package flagparse

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// ParseResult holds the parsed struct and any warnings.
type ParseResult struct {
	Warnings []string
}

// Parse parses a command-line string into a struct value.
// The target must be a pointer to a struct.
//
// Tag format: `flag:"s,long-name"` where s is the short flag and
// long-name is the long flag. Either can be omitted.
// Fields without a flag tag are treated as positional arguments
// in declaration order.
func Parse(target any, args []string) (*ParseResult, error) {
	v := reflect.ValueOf(target)
	if v.Kind() != reflect.Ptr || v.Elem().Kind() != reflect.Struct {
		return nil, fmt.Errorf("target must be a pointer to a struct, got %T", target)
	}

	elem := v.Elem()
	t := elem.Type()

	// Build flag registry.
	type flagInfo struct {
		idx       int
		short     string
		long      string
		fieldType reflect.Type
	}
	var flags []flagInfo
	var positionalFields []int // field indices for positional args

	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := field.Tag.Get("flag")
		if tag == "" && tag != "-" {
			// No tag = positional argument.
			if field.Name != "" && field.PkgPath == "" {
				positionalFields = append(positionalFields, i)
			}
			continue
		}
		if tag == "-" {
			continue
		}

		parts := strings.Split(tag, ",")
		info := flagInfo{idx: i, fieldType: field.Type}
		var longs []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			if len(p) == 1 {
				info.short = p
			} else {
				longs = append(longs, p)
			}
		}
		if len(longs) > 0 {
			info.long = longs[0]
		}
		flags = append(flags, info)
	}

	// Build lookup maps.
	shortMap := make(map[string]flagInfo)
	longMap := make(map[string]flagInfo)
	for _, f := range flags {
		if f.short != "" {
			shortMap[f.short] = f
		}
		if f.long != "" {
			longMap[f.long] = f
		}
	}
	// Register additional aliases (all long names after the first).
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("flag")
		if tag == "" || tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		for idx, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" || len(p) == 1 || idx == 0 {
				continue
			}
			// idx > 0 and len > 1 → additional long alias.
			for _, f := range flags {
				if f.idx == i {
					longMap[p] = f
					break
				}
			}
		}
	}

	result := &ParseResult{}
	posIdx := 0
	havePositional := false
	terminated := false // set by --

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if terminated {
			// Everything after -- is positional.
			if posIdx < len(positionalFields) {
				if err := setField(elem.Field(positionalFields[posIdx]), arg); err != nil {
					return nil, fmt.Errorf("positional arg %d: %w", posIdx+1, err)
				}
				posIdx++
			}
			continue
		}

		if arg == "--" {
			terminated = true
			continue
		}

		if strings.HasPrefix(arg, "--") {
			// Long flag: --name or --name=value
			name := arg[2:]
			var value string
			if eqIdx := strings.Index(name, "="); eqIdx >= 0 {
				value = name[eqIdx+1:]
				name = name[:eqIdx]
			}

			info, ok := longMap[name]
			if !ok {
				result.Warnings = append(result.Warnings, fmt.Sprintf("unknown flag --%s", name))
				continue
			}

			if havePositional {
				result.Warnings = append(result.Warnings, fmt.Sprintf("flag --%s after positional argument", name))
			}

			if eqIdx := strings.Index(arg[2:], "="); eqIdx >= 0 {
				// --name=value form
				if err := setField(elem.Field(info.idx), value); err != nil {
					return nil, fmt.Errorf("flag --%s: %w", name, err)
				}
			} else {
				// --name value form
				if isBoolField(info.fieldType) {
					setBoolField(elem.Field(info.idx), true)
				} else {
					if i+1 >= len(args) {
						return nil, fmt.Errorf("flag --%s requires a value", name)
					}
					i++
					if err := setField(elem.Field(info.idx), args[i]); err != nil {
						return nil, fmt.Errorf("flag --%s: %w", name, err)
					}
				}
			}
			continue
		}

		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			// Short flag(s): -r, -rf, -r -f, -r value
			chars := arg[1:]
			for j := 0; j < len(chars); j++ {
				short := string(chars[j])
				info, ok := shortMap[short]
				if !ok {
					result.Warnings = append(result.Warnings, fmt.Sprintf("unknown flag -%s", short))
					continue
				}

				if havePositional {
					result.Warnings = append(result.Warnings, fmt.Sprintf("flag -%s after positional argument", short))
				}

				if isBoolField(info.fieldType) {
					setBoolField(elem.Field(info.idx), true)
				} else {
					// Non-bool: remaining chars are value, or next arg is value.
					if j+1 < len(chars) {
						value := chars[j+1:]
						if err := setField(elem.Field(info.idx), value); err != nil {
							return nil, fmt.Errorf("flag -%s: %w", short, err)
						}
						break // consumed rest
					}
					if i+1 >= len(args) {
						return nil, fmt.Errorf("flag -%s requires a value", short)
					}
					i++
					if err := setField(elem.Field(info.idx), args[i]); err != nil {
						return nil, fmt.Errorf("flag -%s: %w", short, err)
					}
				}
			}
			continue
		}

		// Positional argument.
		havePositional = true
		if posIdx < len(positionalFields) {
			if err := setField(elem.Field(positionalFields[posIdx]), arg); err != nil {
				return nil, fmt.Errorf("positional arg %d: %w", posIdx+1, err)
			}
			posIdx++
		}
	}

	return result, nil
}

// ParseCommand parses a single command string by first splitting it into
// arguments (respecting quotes) then calling Parse.
// The first argument (command name) is skipped, as the remaining args are
// flags and positional arguments.
func ParseCommand(target any, cmd string) (*ParseResult, error) {
	args, err := SplitArgs(cmd)
	if err != nil {
		return nil, err
	}
	if len(args) > 0 {
		args = args[1:] // skip command name (e.g., "rm" in "rm -rf build/")
	}
	return Parse(target, args)
}

// SplitArgs splits a command string into arguments, respecting single and
// double quotes. Returns error on unbalanced quotes.
func SplitArgs(s string) ([]string, error) {
	var args []string
	var current strings.Builder
	inSingleQuote := false
	inDoubleQuote := false

	for i := 0; i < len(s); i++ {
		c := s[i]

		if inSingleQuote {
			if c == '\'' {
				inSingleQuote = false
			} else {
				current.WriteByte(c)
			}
			continue
		}

		if inDoubleQuote {
			if c == '"' {
				inDoubleQuote = false
			} else if c == '\\' && i+1 < len(s) {
				// Basic escape within double quotes.
				next := s[i+1]
				if next == '"' || next == '\\' || next == '$' || next == '`' || next == '\n' {
					current.WriteByte(next)
					i++
				} else {
					current.WriteByte(c)
				}
			} else {
				current.WriteByte(c)
			}
			continue
		}

		if c == '\'' {
			inSingleQuote = true
			continue
		}
		if c == '"' {
			inDoubleQuote = true
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteByte(c)
	}

	if current.Len() > 0 {
		args = append(args, current.String())
	}

	if inSingleQuote || inDoubleQuote {
		return nil, fmt.Errorf("unbalanced quotes in command")
	}

	return args, nil
}

func isBoolField(t reflect.Type) bool {
	return t.Kind() == reflect.Bool
}

func setBoolField(fv reflect.Value, v bool) {
	fv.SetBool(v)
}

func setField(fv reflect.Value, s string) error {
	if !fv.CanSet() {
		return fmt.Errorf("field is not settable")
	}

	switch fv.Kind() {
	case reflect.String:
		// Strip surrounding quotes that may survive --key="value" splitting.
		fv.SetString(strings.Trim(s, `"'`))
		return nil
	case reflect.Bool:
		// For bool fields, "true", "1", "yes" are true; empty or "false", "0", "no" are false.
		switch strings.ToLower(s) {
		case "true", "1", "yes", "on":
			fv.SetBool(true)
		case "false", "0", "no", "off", "":
			fv.SetBool(false)
		default:
			return fmt.Errorf("cannot parse %q as bool", s)
		}
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return fmt.Errorf("cannot parse %q as int: %w", s, err)
		}
		fv.SetInt(v)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return fmt.Errorf("cannot parse %q as uint: %w", s, err)
		}
		fv.SetUint(v)
		return nil
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return fmt.Errorf("cannot parse %q as float: %w", s, err)
		}
		fv.SetFloat(v)
		return nil
	case reflect.Slice:
		if fv.Type().Elem().Kind() == reflect.String {
			// Append string to slice.
			fv.Set(reflect.Append(fv, reflect.ValueOf(s)))
			return nil
		}
	}

	return fmt.Errorf("unsupported field type: %s", fv.Type())
}
