// Package dbmanager owns connection profiles and credentials for persist
// network backends (workflow decision point 1). A profile mirrors the
// non-secret fields of persist.PersistConfig; secrets live in a separate
// credential map inside the actor's own persist document and are only ever
// returned through the internal profile_resolve surface (the
// aimanager provider_resolve_token analog) or the process-wide dial-time
// resolver installed at startup.
//
// Topology: single global system actor (canonical service name "dbmanager").
// There are no children — profiles are data, not actors.
package dbmanager

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/persist"
	"github.com/qomos-w/sporemind/pkg/policy"
)

// dbCredential is the secret half of a profile, stored apart from the
// non-secret DbProfile fields (sshmanager host/credentials split). Like
// sshmanager, at-rest protection is the actor data directory, not field
// encryption — no encryption precedent exists elsewhere in the repo.
type dbCredential struct {
	Password string `json:"password,omitempty"`
	Secret   string `json:"secret,omitempty"`
	Token    string `json:"token,omitempty"`
}

func (c dbCredential) empty() bool {
	return c.Password == "" && c.Secret == "" && c.Token == ""
}

// Actor owns the connection profiles and credentials.
type Actor struct {
	actor.Host
	store persist.Persist

	// mu guards Profiles/Credentials. Callable handlers run serialized on
	// the actor lane, but the credential resolver is invoked concurrently
	// from persist backend dial goroutines — reads take RLock.
	mu          sync.RWMutex
	Profiles    []domain.DbProfile
	Credentials map[string]dbCredential
}

var _ persist.Persistent = (*Actor)(nil)

type managerSnapshot struct {
	Profiles    []domain.DbProfile      `json:"profiles"`
	Credentials map[string]dbCredential `json:"credentials,omitempty"`
}

// NewActor returns an actor constructor for the global dbmanager actor.
func NewActor() func() actor.Actor {
	return func() actor.Actor {
		return &Actor{}
	}
}

func (a *Actor) Type() string { return "dbmanager" }

func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("dbmanager"))
		if err != nil {
			return err
		}
	}
	a.Credentials = make(map[string]dbCredential)
	if err := a.Load(); err != nil {
		ctx.Logger().Error("dbmanager: load state failed", "error", err)
	}
	return nil
}

func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("dbmanager: starting", "profiles", len(a.Profiles))

	if err := ctx.Register("dbmanager.profile_save", a.handleProfileSave, actor.AdminOnly()); err != nil {
		return fmt.Errorf("dbmanager: register profile.save: %w", err)
	}
	if err := ctx.Register("dbmanager.profile_list", a.handleProfileList, actor.AdminOnly()); err != nil {
		return fmt.Errorf("dbmanager: register profile.list: %w", err)
	}
	if err := ctx.Register("dbmanager.profile_get", a.handleProfileGet, actor.AdminOnly()); err != nil {
		return fmt.Errorf("dbmanager: register profile.get: %w", err)
	}
	if err := ctx.Register("dbmanager.profile_remove", a.handleProfileRemove, actor.AdminOnly()); err != nil {
		return fmt.Errorf("dbmanager: register profile.remove: %w", err)
	}
	// Internal: raw secrets cross this surface. Frontend codegen must never
	// see it, and the handler itself stays ungated by human role so persist
	// backends (system role) can resolve at dial time.
	if err := ctx.Register("dbmanager.profile_resolve", a.handleProfileResolve, actor.Internal()); err != nil {
		return fmt.Errorf("dbmanager: register profile.resolve: %w", err)
	}
	// Internal: dbclient (and any peer dialer) reads the non-secret dial
	// fields of a profile through this surface; the credential itself still
	// resolves at dial time via the process-wide resolver below, never here.
	if err := ctx.Register("dbmanager.profile_lookup", a.handleProfileLookup, actor.Internal()); err != nil {
		return fmt.Errorf("dbmanager: register profile.lookup: %w", err)
	}
	if err := ctx.RegisterDomain("dbmanager").Expose(); err != nil {
		return fmt.Errorf("dbmanager: expose service: %w", err)
	}

	// Install the process-wide dial-time credential resolver. Resolution is
	// uncached: a rotated credential takes effect on the next reconnect.
	// A restart (or another actor's startup) replaces the closure, so the
	// resolver always reads the live owning actor's state.
	persist.SetCredentialResolver(a.resolveCredentialRef)
	return nil
}

// Save persists the actor's durable state.
func (a *Actor) Save() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.saveLocked()
}

// Load restores the actor's durable state.
func (a *Actor) Load() error {
	var snap managerSnapshot
	if err := persist.LoadOrZero(a.store, "dbmanager", &snap); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Profiles = snap.Profiles
	a.Credentials = snap.Credentials
	if a.Credentials == nil {
		a.Credentials = make(map[string]dbCredential)
	}
	return nil
}

// resolveCredentialRef is the persist.CredentialResolver implementation:
// it maps a CredentialRef (a profile Id) to the backend-agnostic
// persist.Credential dial backends consume.
func (a *Actor) resolveCredentialRef(ref string) (persist.Credential, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	p := a.findProfile(ref)
	if p == nil {
		return persist.Credential{}, fmt.Errorf("dbmanager: connection profile %q not found", ref)
	}
	c := a.Credentials[ref]
	return persist.Credential{
		Username:  p.Username,
		Password:  c.Password,
		AccessKey: p.AccessKey,
		Secret:    c.Secret,
		Token:     c.Token,
	}, nil
}

// findProfile returns the profile with the given Id, or nil. Callers must
// hold a.mu (at least RLock).
func (a *Actor) findProfile(id string) *domain.DbProfile {
	for i := range a.Profiles {
		if a.Profiles[i].ID == id {
			return &a.Profiles[i]
		}
	}
	return nil
}

// profileView projects a profile to its masked public shape: secrets become
// Has* presence flags so an edit form can offer "keep existing" without
// ever receiving the value.
func (a *Actor) profileView(p domain.DbProfile) domain.DbProfileView {
	c := a.Credentials[p.ID]
	return domain.DbProfileView{
		ID:          p.ID,
		Name:        p.Name,
		Backend:     p.Backend,
		Endpoint:    p.Endpoint,
		Database:    p.Database,
		TunnelRef:   p.TunnelRef,
		Username:    p.Username,
		AccessKey:   p.AccessKey,
		HasPassword: c.Password != "",
		HasSecret:   c.Secret != "",
		HasToken:    c.Token != "",
	}
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func (a *Actor) handleProfileSave(ctx actor.PureContext, req domain.DbProfileSaveReq) (domain.DbProfileSaveResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.DbProfileSaveResp{}, err
	}
	if req.Name == "" {
		return domain.DbProfileSaveResp{}, fmt.Errorf("dbmanager.profile_save: name is required")
	}
	if req.Backend == "" {
		return domain.DbProfileSaveResp{}, fmt.Errorf("dbmanager.profile_save: backend is required")
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if req.ID == "" {
		req.ID = uuid.NewString()
	}
	p := a.findProfile(req.ID)
	if p == nil {
		p = &domain.DbProfile{ID: req.ID}
		a.Profiles = append(a.Profiles, *p)
		p = &a.Profiles[len(a.Profiles)-1]
	}
	p.Name = req.Name
	p.Backend = req.Backend
	p.Endpoint = req.Endpoint
	p.Database = req.Database
	p.TunnelRef = req.TunnelRef
	p.Username = req.Username
	p.AccessKey = req.AccessKey
	// sshmanager host_update rule: any non-empty secret replaces the whole
	// credential entry; all-empty keeps the existing credential.
	if req.Password != "" || req.Secret != "" || req.Token != "" {
		a.Credentials[req.ID] = dbCredential{Password: req.Password, Secret: req.Secret, Token: req.Token}
	}
	if err := a.saveLocked(); err != nil {
		return domain.DbProfileSaveResp{}, fmt.Errorf("dbmanager.profile_save: save failed: %w", err)
	}
	return domain.DbProfileSaveResp{Profile: a.profileView(*p)}, nil
}

func (a *Actor) handleProfileList(ctx actor.PureContext, _ domain.DbProfileListReq) (domain.DbProfileListResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.DbProfileListResp{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	items := make([]domain.DbProfileView, 0, len(a.Profiles))
	for _, p := range a.Profiles {
		items = append(items, a.profileView(p))
	}
	return domain.DbProfileListResp{Items: items}, nil
}

func (a *Actor) handleProfileGet(ctx actor.PureContext, req domain.DbProfileGetReq) (domain.DbProfileGetResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.DbProfileGetResp{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()
	p := a.findProfile(req.ID)
	if p == nil {
		return domain.DbProfileGetResp{}, fmt.Errorf("dbmanager.profile_get: connection profile %q not found", req.ID)
	}
	return domain.DbProfileGetResp{Profile: a.profileView(*p)}, nil
}

func (a *Actor) handleProfileRemove(ctx actor.PureContext, req domain.DbProfileRemoveReq) (domain.DbProfileRemoveResp, error) {
	if err := policy.RequireAdmin(ctx.Identity().Role); err != nil {
		return domain.DbProfileRemoveResp{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	removed := false
	for i := range a.Profiles {
		if a.Profiles[i].ID == req.ID {
			a.Profiles = append(a.Profiles[:i], a.Profiles[i+1:]...)
			removed = true
			break
		}
	}
	if !removed {
		return domain.DbProfileRemoveResp{}, fmt.Errorf("dbmanager.profile_remove: connection profile %q not found", req.ID)
	}
	delete(a.Credentials, req.ID)
	if err := a.saveLocked(); err != nil {
		return domain.DbProfileRemoveResp{}, fmt.Errorf("dbmanager.profile_remove: save failed: %w", err)
	}
	return domain.DbProfileRemoveResp{}, nil
}

// handleProfileResolve returns the full credential for a profile Id — the
// aimanager provider_resolve_token analog. Internal-only: the response
// carries raw secrets, so it must stay unreachable from any frontend role.
func (a *Actor) handleProfileResolve(_ actor.PureContext, req domain.DbProfileResolveReq) (domain.DbProfileResolveResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	p := a.findProfile(req.ID)
	if p == nil {
		return domain.DbProfileResolveResp{}, fmt.Errorf("dbmanager.profile_resolve: connection profile %q not found", req.ID)
	}
	c := a.Credentials[req.ID]
	return domain.DbProfileResolveResp{
		Username:  p.Username,
		Password:  c.Password,
		AccessKey: p.AccessKey,
		Secret:    c.Secret,
		Token:     c.Token,
	}, nil
}

// handleProfileLookup returns the non-secret half of a profile to peer
// actors (dbclient dials from it). Internal-only like profile_resolve, but
// the response carries no secrets — only backend/endpoint/database/tunnel/
// username/access key; the credential itself resolves at dial time through
// the process-wide credential resolver.
func (a *Actor) handleProfileLookup(_ actor.PureContext, req domain.DbProfileLookupReq) (domain.DbProfileLookupResp, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	p := a.findProfile(req.ID)
	if p == nil {
		return domain.DbProfileLookupResp{}, fmt.Errorf("dbmanager.profile_lookup: connection profile %q not found", req.ID)
	}
	return domain.DbProfileLookupResp{Profile: *p}, nil
}

// saveLocked persists the current state. Callers must hold a.mu.
func (a *Actor) saveLocked() error {
	snap := managerSnapshot{Profiles: a.Profiles, Credentials: a.Credentials}
	return a.store.Save("dbmanager", snap)
}
