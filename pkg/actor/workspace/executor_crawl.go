package workspace

import (
	"context"
	"fmt"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/id"
	"github.com/qomos-w/gospore/ref"
	"github.com/qomos-w/spore/identity"
	"github.com/qomos-w/sporemind/pkg/actor/internal/panicprobe"
	"github.com/qomos-w/sporemind/pkg/actor/project"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/domain/gen"
)

// crawlConfig holds the parsed data.exec fields for a crawl task card.
type crawlConfig struct {
	CrawlCardID string
}

// crawlExecutor implements the crawl execKind. It is a leaf executor: it
// starts a crawl definition card via crawl.start and synchronously
// polls crawl.status until the crawl reaches a terminal state or
// awaiting_login. On done it fetches the full results via
// crawl.results and writes them to the bound task card's
// data.task_outputs so downstream bindings can consume them.
//
// The executor intentionally blocks the workspace owner loop for the
// duration of the crawl; this matches the "start-and-await" semantics of
// the workflow map and keeps failure/cancellation propagation synchronous.
// Long-running crawls are still bounded by the crawl engine's own page
// budget and rate limit.
type crawlExecutor struct {
	a *Actor
}

// newCrawlExecutor wires the executor back to its hosting workspace.
func newCrawlExecutor(a *Actor) *crawlExecutor {
	return &crawlExecutor{a: a}
}

// Kind returns "crawl".
func (e *crawlExecutor) Kind() ExecKind {
	return ExecKindCrawl
}

// Preflight validates the crawl declaration before the dispatcher claims the
// parent task card. Failures here leave the card untouched.
//
// Required configuration from the bound card's data.exec block:
//   - crawl_card_id: id of a type: crawl definition card.
func (e *crawlExecutor) Preflight(ctx actor.PureContext, req ClaimReq) (PreflightResult, error) {
	cfg, err := e.requireCrawlConfig(req)
	if err != nil {
		return PreflightResult{}, err
	}

	projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.crawl: %w", err)
	}

	crawlCardRaw, err := e.a.fetchCardRaw(ctx, projectID, cfg.CrawlCardID)
	if err != nil {
		return PreflightResult{}, fmt.Errorf("workspace.executor.crawl: fetch crawl card %q: %w", cfg.CrawlCardID, err)
	}
	card := project.ParseCardRaw(cfg.CrawlCardID, crawlCardRaw)
	if card.Type != "crawl" {
		return PreflightResult{}, fmt.Errorf("workspace.executor.crawl: card %q has type %q, expected crawl", cfg.CrawlCardID, card.Type)
	}

	if err := requireActiveWorkflow(ctx, "workspace.executor.crawl", req.CallerAgentID); err != nil {
		return PreflightResult{}, err
	}

	return PreflightResult{
		KindConfig:  domain.AgentKindConfig{Kind: domain.AgentKindWorker, DisplayName: "CrawlExecutor"},
		DisplayName: "CrawlExecutor",
		SpawnName:   "crawl-" + cfg.CrawlCardID,
	}, nil
}

// Execute starts the crawl, polls to completion, and maps the crawl state
// back onto the bound task card.
func (e *crawlExecutor) Execute(ctx actor.PureContext, req ClaimReq) (ExecResp, error) {
	return panicprobe.Guard(ctx, "workspace.executor.crawl", req, func() (ExecResp, error) {
		if req.Preflight == nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.crawl: Preflight result is required (dispatcher contract)")
		}
		cfg, err := e.requireCrawlConfig(req)
		if err != nil {
			return ExecResp{}, err
		}

		projectID, err := e.a.resolveWorkflowProjectID(req.ProjectID, req.CallerAgentID)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.crawl: %w", err)
		}

		crawlCardRaw, err := e.a.fetchCardRaw(ctx, projectID, cfg.CrawlCardID)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.crawl: fetch crawl card %q: %w", cfg.CrawlCardID, err)
		}

		crawlRef, ok := ctx.LookupService("crawl")
		if !ok || crawlRef == nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.crawl: crawl actor service not available")
		}

		// Start the crawl task.
		startCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 15*time.Second)
		defer cancel()
		startCall := crawlRef.Invoke(startCtx, "crawl.start", gen.BrowserCrawlStartReq{
			Card: crawlCardRaw,
		})
		if startCall == nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.crawl: crawl.start invoke returned nil")
		}
		result, err := startCall.Final(startCtx)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.crawl: crawl.start failed: %w", err)
		}
		startResp, ok := result.(gen.BrowserCrawlStartResp)
		if !ok {
			return ExecResp{}, fmt.Errorf("workspace.executor.crawl: unexpected crawl.start response %T", result)
		}
		if startResp.TaskID == "" {
			return ExecResp{}, fmt.Errorf("workspace.executor.crawl: crawl.start returned empty task id")
		}

		// Poll until the crawl reaches a state we can map to a task card status.
		finalStatus, err := e.pollCrawlStatus(ctx, crawlRef, startResp.TaskID)
		if err != nil {
			return ExecResp{}, fmt.Errorf("workspace.executor.crawl: poll status for task %q: %w", startResp.TaskID, err)
		}

		// Map crawl state to task card status and outputs.
		switch finalStatus.State {
		case "done":
			results, err := e.fetchCrawlResults(ctx, crawlRef, startResp.TaskID)
			if err != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.crawl: fetch results for task %q: %w", startResp.TaskID, err)
			}
			outputs := map[string]any{"results": results}
			if err := e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, outputs); err != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.crawl: write task outputs for task %q: %w", startResp.TaskID, err)
			}
			if err := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "done"); err != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.crawl: set task card done for task %q: %w", startResp.TaskID, err)
			}
		case "failed":
			note := finalStatus.Error
			if note == "" {
				note = fmt.Sprintf("crawl task %s failed", startResp.TaskID)
			}
			_ = e.setTaskOutputs(ctx, projectID, req.BoundTaskCardID, map[string]any{"error": note})
			if err := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "blocked"); err != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.crawl: set task card failed for task %q: %w", startResp.TaskID, err)
			}
		case "cancelled":
			if err := e.setTaskCardStatus(ctx, projectID, req.BoundTaskCardID, "cancelled"); err != nil {
				return ExecResp{}, fmt.Errorf("workspace.executor.crawl: set task card cancelled for task %q: %w", startResp.TaskID, err)
			}
		default:
			// Should not happen: pollCrawlStatus only returns done/failed/cancelled.
			return ExecResp{}, fmt.Errorf("workspace.executor.crawl: unhandled crawl state %q for task %q", finalStatus.State, startResp.TaskID)
		}

		return ExecResp{
			AgentActorID: startResp.TaskID,
			DisplayName:  "CrawlExecutor",
			Goal: gen.GoalSummary{
				Condition:       req.Body,
				Status:          "active",
				Confirmed:       true,
				BoundTaskCardID: req.BoundTaskCardID,
				MaxTurns:        req.MaxTurns,
			},
		}, nil
	})
}

// requireCrawlConfig parses the required data.exec fields from the bound
// task card raw. It is used by both Preflight and Execute.
func (e *crawlExecutor) requireCrawlConfig(req ClaimReq) (crawlConfig, error) {
	card := project.ParseCardRaw(req.BoundTaskCardID, req.CardRaw)
	execBlock := project.CardDataMap(card, "exec")
	crawlCardID, _ := execBlock["crawl_card_id"].(string)
	if crawlCardID == "" {
		return crawlConfig{}, fmt.Errorf("workspace.executor.crawl: data.exec.crawl_card_id is required")
	}
	return crawlConfig{CrawlCardID: crawlCardID}, nil
}

// pollCrawlStatus polls crawl.status until the crawl task reaches a
// terminal state (done, failed, cancelled). awaiting_login is treated as a
// transient state: the crawl task is paused waiting for user login and will
// resume running once crawl.handoff is confirmed, so the executor keeps
// polling rather than returning early.
func (e *crawlExecutor) pollCrawlStatus(ctx actor.PureContext, crawlRef ref.Ref, taskID string) (gen.BrowserCrawlStatusResp, error) {
	for {
		statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
		statusResp, err := e.callCrawlStatus(statusCtx, crawlRef, taskID)
		cancel()
		if err != nil {
			return gen.BrowserCrawlStatusResp{}, err
		}
		switch statusResp.State {
		case "done", "failed", "cancelled":
			return statusResp, nil
		case "running", "awaiting_login":
			// Transient states: continue polling. awaiting_login is not
			// terminal; the crawl resumes after login handoff.
		default:
			// Unknown state; keep polling defensively.
		}

		select {
		case <-ctx.Lifecycle().Done():
			return gen.BrowserCrawlStatusResp{}, fmt.Errorf("workspace.executor.crawl: context cancelled while polling task %q", taskID)
		case <-time.After(crawlPollInterval):
			// next poll
		}
	}
}

// callCrawlStatus invokes crawl.status and returns the typed response.
func (e *crawlExecutor) callCrawlStatus(ctx context.Context, crawlRef ref.Ref, taskID string) (gen.BrowserCrawlStatusResp, error) {
	call := crawlRef.Invoke(ctx, "crawl.status", gen.BrowserCrawlStatusReq{TaskID: taskID})
	if call == nil {
		return gen.BrowserCrawlStatusResp{}, fmt.Errorf("workspace.executor.crawl: crawl.status invoke returned nil")
	}
	result, err := call.Final(ctx)
	if err != nil {
		return gen.BrowserCrawlStatusResp{}, err
	}
	statusResp, ok := result.(gen.BrowserCrawlStatusResp)
	if !ok {
		return gen.BrowserCrawlStatusResp{}, fmt.Errorf("workspace.executor.crawl: unexpected crawl.status response %T", result)
	}
	return statusResp, nil
}

// fetchCrawlResults drains all results from crawl.results.
func (e *crawlExecutor) fetchCrawlResults(ctx actor.PureContext, crawlRef ref.Ref, taskID string) ([]gen.BrowserCrawlPageResult, error) {
	var all []gen.BrowserCrawlPageResult
	cursor := int32(0)
	const pageSize = 50
	for {
		resultsCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 10*time.Second)
		call := crawlRef.Invoke(resultsCtx, "crawl.results", gen.BrowserCrawlResultsReq{
			TaskID: taskID,
			Cursor: cursor,
			Limit:  pageSize,
		})
		if call == nil {
			cancel()
			return nil, fmt.Errorf("workspace.executor.crawl: crawl.results invoke returned nil")
		}
		result, err := call.Final(resultsCtx)
		cancel()
		if err != nil {
			return nil, err
		}
		resp, ok := result.(gen.BrowserCrawlResultsResp)
		if !ok {
			return nil, fmt.Errorf("workspace.executor.crawl: unexpected crawl.results response %T", result)
		}
		all = append(all, resp.Results...)
		if resp.Done || len(resp.Results) == 0 {
			break
		}
		cursor = resp.NextCursor
	}
	return all, nil
}

// setTaskOutputs writes outputs to the bound task card via
// project.wiki_set_task_outputs.
func (e *crawlExecutor) setTaskOutputs(ctx actor.PureContext, projectID, cardID string, outputs map[string]any) error {
	if projectID == "" || cardID == "" {
		return fmt.Errorf("projectID and cardID are required")
	}
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return err
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return fmt.Errorf("project actor unavailable")
	}
	outputsCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(outputsCtx, "project.wiki_set_task_outputs", domain.WikiSetTaskOutputsReq{
		CardID:  cardID,
		Outputs: outputs,
	})
	if call == nil {
		return fmt.Errorf("project.wiki_set_task_outputs invoke returned nil")
	}
	_, err = call.Final(outputsCtx)
	return err
}

// setTaskCardStatus updates the bound task card status via
// project.wiki_set_status.
func (e *crawlExecutor) setTaskCardStatus(ctx actor.PureContext, projectID, cardID, status string) error {
	if projectID == "" || cardID == "" {
		return fmt.Errorf("projectID and cardID are required")
	}
	cid, err := identity.ParseCanonicalID(projectID)
	if err != nil {
		return err
	}
	projectRef, ok := ctx.LookupID(id.From(cid))
	if !ok || projectRef == nil {
		return fmt.Errorf("project actor unavailable")
	}
	statusCtx, cancel := context.WithTimeout(ctx.Lifecycle(), 5*time.Second)
	defer cancel()
	call := projectRef.Invoke(statusCtx, "project.wiki_set_status", gen.WikiSetStatusReq{
		ID:     cardID,
		Status: status,
	})
	if call == nil {
		return fmt.Errorf("project.wiki_set_status invoke returned nil")
	}
	_, err = call.Final(statusCtx)
	return err
}
