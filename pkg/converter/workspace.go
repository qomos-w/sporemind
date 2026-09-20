package converter

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/qomos-w/sporemind/pkg/domain"
)

func init() {
	Register(reflect.TypeOf(domain.WorkspaceLogsQueryResp{}), logsQueryConverter)
}

// logsQueryConverter formats log query results as readable lines with a
// truncation hint and continuation cursor at the end.
func logsQueryConverter(result any) (string, error) {
	resp, ok := result.(domain.WorkspaceLogsQueryResp)
	if !ok {
		return "", fmt.Errorf("logsQueryConverter: expected WorkspaceLogsQueryResp, got %T", result)
	}
	if len(resp.Items) == 0 {
		return "(no log entries)", nil
	}
	var sb strings.Builder
	for _, e := range resp.Items {
		ts := e.Timestamp
		// Trim sub-second precision clutter: keep the first 19 chars
		// (YYYY-MM-DDTHH:MM:SS) when the full RFC3339Nano is longer.
		if len(ts) > 19 {
			ts = ts[:19]
		}
		level := e.Level
		if level == "" {
			level = "INFO"
		}
		fmt.Fprintf(&sb, "[%s] %s %s: %s\n", ts, strings.ToUpper(level), e.Caller, e.Message)
	}
	if resp.Truncated {
		fmt.Fprintf(&sb, "[truncated: %d entries shown, more available. Use Before=\"%s\" to continue]\n",
			len(resp.Items), resp.NextBefore)
	} else {
		fmt.Fprintf(&sb, "(%d entries)", len(resp.Items))
	}
	return sb.String(), nil
}
