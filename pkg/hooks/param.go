package hooks

// ParamInt32 extracts an int32 from a map[string]any value, returning
// the default if the value is nil or not a recognized numeric type.
func ParamInt32(v any, defaultVal int32) int32 {
	switch n := v.(type) {
	case float64:
		return int32(n)
	case int:
		return int32(n)
	case int32:
		return n
	case int64:
		return int32(n)
	}
	return defaultVal
}
