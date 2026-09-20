package cookiebridge

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// pendingRequest is what the Chrome extension sees on GET /pending: one
// agent-asked import, never containing cookie data.
type pendingRequest struct {
	ID         string    `json:"id"`
	Domain     string    `json:"domain"`
	InstanceID string    `json:"instanceId"`
	CreatedAt  time.Time `json:"createdAt"`
}

// pushOutcome carries the import result back to the waiting import_site call.
type pushOutcome struct {
	imported int64
	skipped  int
	domains  []string
	err      error
}

type pendingEntry struct {
	req  pendingRequest
	done chan pushOutcome // buffered 1
}

// pendingRegistry tracks live import_site waits. In-memory only: a pending
// request exists exactly as long as some import_site call is blocked on it.
type pendingRegistry struct {
	mu      sync.Mutex
	nextID  int
	entries map[string]*pendingEntry
}

func newPendingRegistry() *pendingRegistry {
	return &pendingRegistry{entries: map[string]*pendingEntry{}}
}

func (r *pendingRegistry) add(domain, instanceID string) *pendingEntry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.nextID++
	e := &pendingEntry{
		req: pendingRequest{
			ID:         fmt.Sprintf("cb-%d-%d", time.Now().Unix(), r.nextID),
			Domain:     domain,
			InstanceID: instanceID,
			CreatedAt:  time.Now().UTC(),
		},
		done: make(chan pushOutcome, 1),
	}
	r.entries[e.req.ID] = e
	return e
}

func (r *pendingRegistry) list() []pendingRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]pendingRequest, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.req)
	}
	return out
}

func (r *pendingRegistry) get(id string) (*pendingEntry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	return e, ok
}

func (r *pendingRegistry) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.entries, id)
}

const (
	defaultWaitSeconds = 120
	maxWaitSeconds     = 600
)

// importSiteArgs is the input schema of the MCP import_site tool.
type importSiteArgs struct {
	Domain      string `json:"domain" jsonschema:"target site domain, e.g. github.com — only cookies for this domain are read"`
	InstanceID  string `json:"instanceId,omitempty" jsonschema:"independent browser instance id or name; omit if exactly one exists"`
	WaitSeconds int    `json:"waitSeconds,omitempty" jsonschema:"seconds to wait for the user to confirm in the Chrome extension (default 120, max 600)"`
}

// importSiteResult is the MCP import_site output. Counts and domains only —
// cookie values must never reach LLM context.
type importSiteResult struct {
	Status   string   `json:"status"` // imported | timeout | error
	Imported int64    `json:"imported,omitempty"`
	Skipped  int      `json:"skipped,omitempty"`
	Domains  []string `json:"domains,omitempty"`
	Detail   string   `json:"detail,omitempty"`
}

// statusResult is the MCP status tool output.
type statusResult struct {
	Listening bool             `json:"listening"`
	Addr      string           `json:"addr,omitempty"`
	Pending   []pendingRequest `json:"pending"`
}

// buildMCPServer assembles the in-process MCP server for /mcp.
func (a *Actor) buildMCPServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "cookiebridge", Version: "1.0.0"}, nil)
	mcp.AddTool(s,
		&mcp.Tool{
			Name: "import_site",
			Description: "Migrate Chrome cookies for one site into an independent browser instance (login state transfer). " +
				"Registers the request, then WAITS until the user confirms it in the paired Chrome extension (Cookie Bridge popup) — the extension pushes the cookies, the server converts and imports them, and only counts and domain names are returned. " +
				"Ask the user to open the Cookie Bridge popup before calling. Cookie values never enter the conversation.",
		},
		a.handleImportSite,
	)
	mcp.AddTool(s,
		&mcp.Tool{
			Name:        "status",
			Description: "Report whether the Cookie Bridge listener is up and which import requests are currently waiting for Chrome confirmation.",
		},
		a.handleStatus,
	)
	return s
}

func (a *Actor) handleImportSite(ctx context.Context, _ *mcp.CallToolRequest, args importSiteArgs) (*mcp.CallToolResult, importSiteResult, error) {
	if args.Domain == "" {
		return nil, importSiteResult{Status: "error", Detail: "domain is required"}, nil
	}
	domain := normalizeCookieDomain(args.Domain)

	instanceID := args.InstanceID
	if instanceID == "" {
		resolved, err := a.importer.ResolveInstance(ctx, "")
		if err != nil {
			return nil, importSiteResult{Status: "error", Detail: err.Error()}, nil
		}
		instanceID = resolved
	} else if resolved, err := a.importer.ResolveInstance(ctx, instanceID); err == nil {
		instanceID = resolved
	}

	wait := args.WaitSeconds
	if wait <= 0 {
		wait = defaultWaitSeconds
	}
	if wait > maxWaitSeconds {
		wait = maxWaitSeconds
	}

	entry := a.pending.add(domain, instanceID)
	defer a.pending.remove(entry.req.ID)

	timer := time.NewTimer(time.Duration(wait) * time.Second)
	defer timer.Stop()
	select {
	case out := <-entry.done:
		if out.err != nil {
			return nil, importSiteResult{Status: "error", Detail: out.err.Error()}, nil
		}
		return nil, importSiteResult{
			Status:   "imported",
			Imported: out.imported,
			Skipped:  out.skipped,
			Domains:  out.domains,
		}, nil
	case <-timer.C:
		return nil, importSiteResult{
			Status: "timeout",
			Detail: fmt.Sprintf("no confirmation from the Chrome extension within %ds (pending id %s); ask the user to open the Cookie Bridge popup and approve the request, then retry", wait, entry.req.ID),
		}, nil
	case <-ctx.Done():
		return nil, importSiteResult{Status: "timeout", Detail: "cancelled while waiting for extension confirmation"}, nil
	}
}

func (a *Actor) handleStatus(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, statusResult, error) {
	a.mu.Lock()
	listening := a.server != nil
	addr := a.listenerAddr
	a.mu.Unlock()
	return nil, statusResult{
		Listening: listening,
		Addr:      addr,
		Pending:   a.pending.list(),
	}, nil
}
