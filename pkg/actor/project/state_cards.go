package project

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/util"
)

// This file owns the two ground-truth cards that the project actor previously
// held as authoritative in-memory fields on the Actor struct:
//
//   - configCard (.rconfig): roots, mounted card refs, no-git mode, protected
//     files. Backs sync_roots / card_mount+card_unmount / no_git_mode_set /
//     set_protected_files.
//   - uiStateCard (.ropen): open card IDs in the story river plus the
//     knowledge-base starred card IDs. Backs wiki_save_open_cards /
//     wiki_open_card / wiki_close_card and wiki_get_starred / wiki_set_starred.
//
// Both cards are the authoritative source of truth on disk, written through
// persist.Persist (never raw file IO). Callables read the card fresh on every
// invocation and write through on every mutation; the Actor struct keeps no
// persistent copies of any of this state ("前端状态后端化" + the actor-state
// ownership constraint). A pair of in-memory mutexes serializes only the
// read-modify-write critical sections so concurrent PureContext handlers cannot
// lose updates; the data itself lives exclusively on disk.

// configCard is the project config ground-truth record. Stored via
// persist.Persist under the "<actorID>.rconfig" name.
type configCard struct {
	Roots            []domain.RootDirEntry `json:"roots"`
	MountedCardRefs   []gen.CardRef         `json:"mountedCardRefs,omitempty"`
	NoGitMode        bool                  `json:"noGitMode,omitempty"`
	ProtectedFiles    []string              `json:"protectedFiles,omitempty"`
	// ProtectedFilesByWorktree holds per-worktree protected-file lists,
	// keyed by worktree ID. A worktree-bound caller's dev_generate stores
	// its codegen view here instead of replacing the global ProtectedFiles,
	// so divergent worktree branches cannot clobber the main tree's
	// protections (and vice versa). Entries are cascade-removed when the
	// worktree is deleted. Added after ProtectedFiles; cards written before
	// this field existed load with a nil map, which is fine.
	ProtectedFilesByWorktree map[string][]string `json:"protectedFilesByWorktree,omitempty"`
	// DefaultExtraBundleIDs are merged into every spawn_agent request's
	// ExtraBundleIDs (defaults first, deduped), so agents and sub-agents
	// created under this project mount these bundles in addition to their
	// kind-config defaults. Edited via project.default_bundles_set. Appended
	// last; cards written before this field existed load with nil, which is
	// fine.
	DefaultExtraBundleIDs []string `json:"defaultExtraBundleIDs,omitempty"`
}

// uiStateCard is the UI-state ground-truth record (open cards in the story
// river + knowledge-base starred cards). Stored via persist.Persist under the
// "<actorID>.ropen" name.
type uiStateCard struct {
	OpenCards []string `json:"openCards,omitempty"`
	// Starred is the knowledge-base star list: card ids in this project that
	// the user has starred, most-recently-starred first. Purely UI state — it
	// never rewrites the starred card's own body/frontmatter.
	Starred []string `json:"starred,omitempty"`
}

// configCardName returns the persist record name for the config card.
func (a *Actor) configCardName() string { return a.actorID + ".rconfig" }

// uiStateCardName returns the persist record name for the UI-state card.
func (a *Actor) uiStateCardName() string { return a.actorID + ".ropen" }

// readConfigCard loads the config card from the ground-truth store. A missing
// card (first start or post-drift recovery) yields an empty card and nil.
func (a *Actor) readConfigCard() (configCard, error) {
	if a.persistStore == nil {
		return configCard{}, nil
	}
	var c configCard
	if err := persist.LoadOrZero(a.persistStore, a.configCardName(), &c); err != nil {
		return configCard{}, fmt.Errorf("project: load config card: %w", err)
	}
	return c, nil
}

// writeConfigCard persists the config card to the ground-truth store.
func (a *Actor) writeConfigCard(c configCard) error {
	if a.persistStore == nil {
		return nil
	}
	return a.persistStore.Save(a.configCardName(), c)
}

// readUIStateCard loads the UI-state card from the ground-truth store. A missing
// card yields an empty card and nil.
func (a *Actor) readUIStateCard() (uiStateCard, error) {
	if a.persistStore == nil {
		return uiStateCard{}, nil
	}
	var c uiStateCard
	if err := persist.LoadOrZero(a.persistStore, a.uiStateCardName(), &c); err != nil {
		return uiStateCard{}, fmt.Errorf("project: load ui-state card: %w", err)
	}
	return c, nil
}

// writeUIStateCard persists the UI-state card to the ground-truth store.
func (a *Actor) writeUIStateCard(c uiStateCard) error {
	if a.persistStore == nil {
		return nil
	}
	return a.persistStore.Save(a.uiStateCardName(), c)
}

// configSnapshot returns a private copy of the config card taken under
// configMu so PureContext readers (file/root handlers run off the owner
// queue) cannot race the config RMW writers. The card is read fresh from disk
// every call — there is no in-memory authoritative copy.
func (a *Actor) configSnapshot() (configCard, error) {
	a.configMu.RLock()
	defer a.configMu.RUnlock()
	c, err := a.readConfigCard()
	if err != nil {
		return configCard{}, err
	}
	roots := make([]domain.RootDirEntry, len(c.Roots))
	copy(roots, c.Roots)
	c.Roots = roots
	refs := make([]gen.CardRef, len(c.MountedCardRefs))
	copy(refs, c.MountedCardRefs)
	c.MountedCardRefs = refs
	pf := make([]string, len(c.ProtectedFiles))
	copy(pf, c.ProtectedFiles)
	c.ProtectedFiles = pf
	db := make([]string, len(c.DefaultExtraBundleIDs))
	copy(db, c.DefaultExtraBundleIDs)
	c.DefaultExtraBundleIDs = db
	if c.ProtectedFilesByWorktree != nil {
		buckets := make(map[string][]string, len(c.ProtectedFilesByWorktree))
		for id, files := range c.ProtectedFilesByWorktree {
			cp := make([]string, len(files))
			copy(cp, files)
			buckets[id] = cp
		}
		c.ProtectedFilesByWorktree = buckets
	}
	return c, nil
}

// rootsSnapshot is a convenience reader returning just the roots slice, read
// fresh from the ground-truth config card. Used by hot-path root resolvers
// (fileops, filewatcher, git/shell root selection).
func (a *Actor) rootsSnapshot() ([]domain.RootDirEntry, error) {
	c, err := a.configSnapshot()
	if err != nil {
		return nil, err
	}
	return c.Roots, nil
}

// mountedCardRefsSnapshot returns a private copy of the mounted card refs,
// read fresh from the config card.
func (a *Actor) mountedCardRefsSnapshot() ([]gen.CardRef, error) {
	c, err := a.configSnapshot()
	if err != nil {
		return nil, err
	}
	return c.MountedCardRefs, nil
}

// noGitModeSnapshot returns the persisted no-git mode flag from the config
// card.
func (a *Actor) noGitModeSnapshot() (bool, error) {
	c, err := a.configSnapshot()
	if err != nil {
		return false, err
	}
	return c.NoGitMode, nil
}

// protectedFilesSnapshot returns a private copy of the global protected-file
// paths from the config card. Worktree-scoped buckets are exposed via
// configSnapshot / protectedPathsFor instead.
func (a *Actor) protectedFilesSnapshot() ([]string, error) {
	c, err := a.configSnapshot()
	if err != nil {
		return nil, err
	}
	return c.ProtectedFiles, nil
}

// openCardsSnapshot returns a private copy of the open-card IDs, read fresh
// from the UI-state card under uiStateMu.
func (a *Actor) openCardsSnapshot() ([]string, error) {
	a.uiStateMu.RLock()
	defer a.uiStateMu.RUnlock()
	c, err := a.readUIStateCard()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(c.OpenCards))
	copy(out, c.OpenCards)
	return out, nil
}

// starredSnapshot returns a private copy of the knowledge-base starred card
// IDs, read fresh from the UI-state card under uiStateMu.
func (a *Actor) starredSnapshot() ([]string, error) {
	a.uiStateMu.RLock()
	defer a.uiStateMu.RUnlock()
	c, err := a.readUIStateCard()
	if err != nil {
		return nil, err
	}
	out := make([]string, len(c.Starred))
	copy(out, c.Starred)
	return out, nil
}

// updateConfigCard runs mut over the freshly-loaded ground-truth config card
// and persists the result. The whole read-modify-write is serialized by
// configMu so concurrent PureContext writers cannot interleave and lose an
// append.
func (a *Actor) updateConfigCard(mut func(c *configCard)) error {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	c, err := a.readConfigCard()
	if err != nil {
		return err
	}
	mut(&c)
	return a.writeConfigCard(c)
}

// updateOpenCards runs mut over the freshly-loaded open-card list and persists
// the result, serialized by uiStateMu.
func (a *Actor) updateOpenCards(mut func(cards *[]string)) error {
	a.uiStateMu.Lock()
	defer a.uiStateMu.Unlock()
	c, err := a.readUIStateCard()
	if err != nil {
		return err
	}
	mut(&c.OpenCards)
	return a.writeUIStateCard(c)
}

// updateStarred runs mut over the freshly-loaded starred-card list and persists
// the result, serialized by uiStateMu (same critical section as updateOpenCards
// so a star toggle cannot clobber a concurrent open/close write).
func (a *Actor) updateStarred(mut func(starred *[]string)) error {
	a.uiStateMu.Lock()
	defer a.uiStateMu.Unlock()
	c, err := a.readUIStateCard()
	if err != nil {
		return err
	}
	mut(&c.Starred)
	return a.writeUIStateCard(c)
}

// ensureConfigCardSeed seeds the config card with a default root when it is
// empty and an initial path is known. Idempotent; writes through so the disk
// card carries the seed from the first start.
func (a *Actor) ensureConfigCardSeed() error {
	c, err := a.readConfigCard()
	if err != nil {
		return err
	}
	if len(c.Roots) == 0 && a.initialPath != "" {
		c.Roots = []domain.RootDirEntry{{Name: "default", Path: a.initialPath}}
		return a.writeConfigCard(c)
	}
	return nil
}

// migrateLegacyStateCards performs the one-time import of pre-refactor state
// into the ground-truth cards so a restart across the refactor boundary keeps
// the user's roots, mounted cards, no-git flag, protected files, and open
// cards. Each legacy source is consulted only when the corresponding card does
// not yet exist. Legacy files are left in place (harmless; the new cards are
// authoritative from the first write).
//
// Legacy sources:
//   - config: the old "<actorID>" persist blob (persistState with
//     roots/mountedCardRefs/noGitMode) and, older still, the local
//     .sporecode/state.json; protected files came from
//     .sporecode/generated-files.json.
//   - ui-state: .sporecode/wiki-state.json.
func (a *Actor) migrateLegacyStateCards() error {
	if a.persistStore == nil {
		return nil
	}
	if err := a.migrateLegacyConfigCard(); err != nil {
		return err
	}
	return a.migrateLegacyUIStateCard()
}

func (a *Actor) migrateLegacyConfigCard() error {
	var existing configCard
	if err := a.persistStore.Load(a.configCardName(), &existing); err == nil {
		// Card already exists; nothing to migrate.
		return nil
	} else if !errors.Is(err, persist.ErrNotExist) {
		return fmt.Errorf("project: probe config card for migration: %w", err)
	}

	// 1) Old "<actorID>" persist blob (persistState: roots / mountedCardRefs /
	//    noGitMode).
	card := configCard{}
	var legacy persistState
	if err := a.persistStore.Load(a.actorID, &legacy); err == nil {
		card.Roots = legacy.Roots
		card.MountedCardRefs = legacy.MountedCardRefs
		card.NoGitMode = legacy.NoGitMode
	} else if !errors.Is(err, persist.ErrNotExist) {
		return fmt.Errorf("project: load legacy state for migration: %w", err)
	}

	// 2) Older local .sporecode/state.json fallback (covers the pre-global-store
	//    era and bare-array roots). Only consulted if the blob path did not yield
	//    roots, matching the original Load() precedence.
	if len(card.Roots) == 0 {
		if data, rerr := os.ReadFile(a.statePath()); rerr == nil {
			var oldState persistState
			if jerr := json.Unmarshal(data, &oldState); jerr == nil && oldState.Roots != nil {
				card.Roots = oldState.Roots
				card.NoGitMode = oldState.NoGitMode
			} else {
				var roots []domain.RootDirEntry
				if jerr2 := json.Unmarshal(data, &roots); jerr2 == nil {
					card.Roots = roots
				}
			}
		} else if !os.IsNotExist(rerr) {
			return fmt.Errorf("project: read legacy state.json for migration: %w", rerr)
		}
	}

	// 3) Protected files from .sporecode/generated-files.json (raw IO in the
	//    old design; now folded into the config card).
	if data, rerr := os.ReadFile(a.protectedFilesPath()); rerr == nil {
		var paths []string
		if jerr := json.Unmarshal(data, &paths); jerr == nil {
			cleaned := make([]string, 0, len(paths))
			for _, p := range paths {
				cleaned = append(cleaned, filepath.Clean(p))
			}
			card.ProtectedFiles = cleaned
		}
	} else if !os.IsNotExist(rerr) {
		return fmt.Errorf("project: read legacy generated-files.json for migration: %w", rerr)
	}

	// Only persist when we actually found something to import; otherwise the
	// seed step (ensureConfigCardSeed) creates the initial card.
	if len(card.Roots) > 0 || len(card.MountedCardRefs) > 0 || card.NoGitMode || len(card.ProtectedFiles) > 0 {
		return a.writeConfigCard(card)
	}
	return nil
}

func (a *Actor) migrateLegacyUIStateCard() error {
	var existing uiStateCard
	if err := a.persistStore.Load(a.uiStateCardName(), &existing); err == nil {
		return nil
	} else if !errors.Is(err, persist.ErrNotExist) {
		return fmt.Errorf("project: probe ui-state card for migration: %w", err)
	}

	var cards []string
	if data, rerr := os.ReadFile(a.openCardsPath()); rerr == nil {
		if jerr := json.Unmarshal(data, &cards); jerr != nil {
			return fmt.Errorf("project: parse legacy wiki-state.json for migration: %w", jerr)
		}
	} else if !os.IsNotExist(rerr) {
		return fmt.Errorf("project: read legacy wiki-state.json for migration: %w", rerr)
	}
	if len(cards) > 0 {
		return a.writeUIStateCard(uiStateCard{OpenCards: cards})
	}
	return nil
}

var (
	_ = actor.Host{}
	_ = util.NormalizePath
)

// persistState is the pre-refactor actor-state envelope that used to be stored
// as the "<actorID>" persist record (roots / mountedCardRefs / noGitMode). It
// survives only as the legacy-migration source type read by
// migrateLegacyConfigCard; nothing writes it anymore.
type persistState struct {
	Roots           []domain.RootDirEntry `json:"roots"`
	MountedCardRefs []gen.CardRef         `json:"mountedCardRefs,omitempty"`
	NoGitMode       bool                  `json:"noGitMode,omitempty"`
}
