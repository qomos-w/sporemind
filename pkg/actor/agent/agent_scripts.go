package agent

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
)

// savedScript is one accumulated spore snippet. The agent curates these
// itself: save upserts by Name (edit is the same entry), delete removes,
// and the whole set is injected into every turn's hot context so the LLM
// can reuse a snippet by re-submitting its (possibly edited) body through
// eval_script.
type savedScript struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Script      string `json:"script"`
}

// savedScriptsDoc is the single JSON document persisted under the agent's
// own namespace (agent/<actorID>/scripts).
type savedScriptsDoc struct {
	Scripts map[string]savedScript `json:"scripts"`
}

const maxSavedScriptBytes = 16 * 1024
const maxSavedScripts = 64

// savedScriptsMu guards savedScripts: script_save/script_delete run on the
// owner lane, while buildSavedScriptsBlock reads from resolveHotContext
// (which pure-context callers assemble concurrently).
var savedScriptsMu sync.Mutex

// scriptsStore returns the agent persist backend handle for the scripts doc.
func (a *Actor) scriptsStore() persist.Persist {
	return persist.MustNew(config.PersistConfig("agent"))
}

func (a *Actor) scriptsDocName() string {
	return a.agentStoreKey() + "/scripts"
}

// loadSavedScripts restores the accumulated script set from the agent's
// persist namespace. A missing doc (first run) is not an error.
func (a *Actor) loadSavedScripts() error {
	savedScriptsMu.Lock()
	defer savedScriptsMu.Unlock()
	if a.savedScripts != nil {
		return nil
	}
	a.savedScripts = map[string]savedScript{}
	var doc savedScriptsDoc
	if err := a.scriptsStore().Load(a.scriptsDocName(), &doc); err != nil {
		if err == persist.ErrNotExist {
			return nil
		}
		return fmt.Errorf("load saved scripts: %w", err)
	}
	for name, s := range doc.Scripts {
		a.savedScripts[name] = s
	}
	return nil
}

// persistSavedScripts writes the whole set back as one document. Called with
// savedScriptsMu held.
func (a *Actor) persistSavedScripts() error {
	if a.savedScripts == nil {
		a.savedScripts = map[string]savedScript{}
	}
	return a.scriptsStore().Save(a.scriptsDocName(), savedScriptsDoc{Scripts: a.savedScripts})
}

// handleScriptSave backs the agent-local script_save: upsert one spore
// snippet by Name. Stateful (owner lane): low-frequency mutate that writes
// actor state and persists — exactly the category the owner lane is for.
func (a *Actor) handleScriptSave(_ actor.Context, req domain.AgentScriptSaveReq) (domain.AgentScriptSaveResp, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return domain.AgentScriptSaveResp{}, fmt.Errorf("script_save: Name is required")
	}
	if strings.TrimSpace(req.Script) == "" {
		return domain.AgentScriptSaveResp{}, fmt.Errorf("script_save: Script is required")
	}
	if len(req.Script) > maxSavedScriptBytes {
		return domain.AgentScriptSaveResp{}, fmt.Errorf("script_save: Script exceeds %d bytes", maxSavedScriptBytes)
	}

	savedScriptsMu.Lock()
	defer savedScriptsMu.Unlock()
	if a.savedScripts == nil {
		a.savedScripts = map[string]savedScript{}
	}
	updated := false
	if _, exists := a.savedScripts[name]; exists {
		updated = true
	} else if len(a.savedScripts) >= maxSavedScripts {
		return domain.AgentScriptSaveResp{}, fmt.Errorf("script_save: script quota full (%d); delete one first", maxSavedScripts)
	}
	a.savedScripts[name] = savedScript{Name: name, Description: strings.TrimSpace(req.Description), Script: req.Script}
	if err := a.persistSavedScripts(); err != nil {
		// Roll back the in-memory copy so state and disk never diverge.
		if !updated {
			delete(a.savedScripts, name)
		}
		return domain.AgentScriptSaveResp{}, fmt.Errorf("script_save: %w", err)
	}
	return domain.AgentScriptSaveResp{Name: name, Updated: updated}, nil
}

// handleScriptDelete backs the agent-local script_delete: remove one saved
// snippet by Name. Unknown names are an explicit error. Stateful (owner
// lane).
func (a *Actor) handleScriptDelete(_ actor.Context, req domain.AgentScriptDeleteReq) (domain.AgentScriptDeleteResp, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return domain.AgentScriptDeleteResp{}, fmt.Errorf("script_delete: Name is required")
	}

	savedScriptsMu.Lock()
	defer savedScriptsMu.Unlock()
	if a.savedScripts == nil {
		a.savedScripts = map[string]savedScript{}
	}
	if _, exists := a.savedScripts[name]; !exists {
		return domain.AgentScriptDeleteResp{}, fmt.Errorf("script_delete: %q not found", name)
	}
	prev := a.savedScripts[name]
	delete(a.savedScripts, name)
	if err := a.persistSavedScripts(); err != nil {
		a.savedScripts[name] = prev
		return domain.AgentScriptDeleteResp{}, fmt.Errorf("script_delete: %w", err)
	}
	return domain.AgentScriptDeleteResp{Deleted: true}, nil
}

// buildSavedScriptsBlock renders the accumulated script set as a hot-context
// block: every snippet's name, description, and full body, sorted by name,
// so the model can re-run one through eval_script (editing freely) without
// any lookup call. Returns nil when nothing is saved.
func (a *Actor) buildSavedScriptsBlock() *domain.ContentBlock {
	savedScriptsMu.Lock()
	names := make([]string, 0, len(a.savedScripts))
	scripts := make(map[string]savedScript, len(a.savedScripts))
	for name, s := range a.savedScripts {
		names = append(names, name)
		scripts[name] = s
	}
	savedScriptsMu.Unlock()
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString("## Saved Scripts\n\n")
	sb.WriteString("Your accumulated spore snippets. Re-run one via eval_script with its body (edit freely); script_save upserts by Name, script_delete removes.\n")
	for _, name := range names {
		s := scripts[name]
		sb.WriteString("\n### " + s.Name + "\n")
		if s.Description != "" {
			sb.WriteString(s.Description + "\n")
		}
		sb.WriteString("```spore\n" + s.Script + "\n```\n")
	}
	return &domain.ContentBlock{Type: "text", Text: sb.String()}
}
