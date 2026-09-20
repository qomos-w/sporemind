package user

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/gospore/resource"
	"github.com/qomos-w/sporemind/pkg/auth"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/persist"
)

// DesktopTokenIssuer issues a JWT for the local admin without a password.
// Exposed via the app resource registry so in-process callers with
// process-level trust (e.g. the Wails desktop binary) can auto-login.
type DesktopTokenIssuer func() (string, error)

// ---------------------------------------------------------------------------
// Protocol types
// ---------------------------------------------------------------------------
// All request/response and component types are generated from schemas/user.spore.
// Import path: github.com/qomos-w/sporemind/pkg/domain/gen/user (aliased as usergen).

// ---------------------------------------------------------------------------
// Actor
// ---------------------------------------------------------------------------

// Actor manages user accounts, groups, and permissions.
type Actor struct {
	actor.Host
	store         persist.Persist
	Accounts      []gen.Account           `gospore:"component,admin"`
	RefreshTokens []gen.RefreshTokenEntry `gospore:"component,admin"`
	Groups        []gen.Group             `gospore:"component,public"`
	Permissions   gen.PermissionMatrix    `gospore:"component,public"`

	actorID string
	jwt     *auth.Manager
	mu      sync.RWMutex
}

var _ persist.Persistent = (*Actor)(nil)

// Type returns the actor type identifier.
func (a *Actor) Type() string { return "user" }

// OnInit loads persisted state and initializes the JWT manager.
func (a *Actor) OnInit(ctx actor.Context) error {
	var err error
	a.store, err = persist.New(config.PersistConfig("user"))
	if err != nil {
		return err
	}
	a.actorID = ctx.Self().ID().String()
	a.jwt = auth.NewManager(auth.DefaultJWTConfig())
	if err := a.Load(); err != nil {
		ctx.Logger().Error("user: load state failed", "error", err)
	}
	a.cleanupExpiredRefreshTokens()
	a.seedDefaults(ctx)
	return nil
}

// OnStart registers callables.
func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("user: starting", "id", a.actorID)

	// Auth callables (public — no token needed)
	if err := ctx.Register("user.auth_register", a.handleRegister, actor.Public()); err != nil {
		return fmt.Errorf("user: register auth.register: %w", err)
	}
	if err := ctx.Register("user.auth_login", a.handleLogin, actor.Public()); err != nil {
		return fmt.Errorf("user: register auth.login: %w", err)
	}
	if err := ctx.Register("user.auth_refresh", a.handleRefresh, actor.Public()); err != nil {
		return fmt.Errorf("user: register auth.refresh: %w", err)
	}
	if err := ctx.Register("user.auth_me", a.handleMe, actor.Public()); err != nil {
		return fmt.Errorf("user: register auth.me: %w", err)
	}

	// Account management (token-protected, admin to mutate)
	if err := ctx.Register("user.list", a.handleListAccounts, actor.Public()); err != nil {
		return fmt.Errorf("user: register list: %w", err)
	}
	if err := ctx.Register("user.create", a.handleCreateAccount, actor.Public()); err != nil {
		return fmt.Errorf("user: register create: %w", err)
	}
	if err := ctx.Register("user.update", a.handleUpdateAccount, actor.Public()); err != nil {
		return fmt.Errorf("user: register update: %w", err)
	}
	if err := ctx.Register("user.remove", a.handleRemoveAccount, actor.Public()); err != nil {
		return fmt.Errorf("user: register remove: %w", err)
	}
	if err := ctx.Register("user.reset_password", a.handleResetPassword, actor.Public()); err != nil {
		return fmt.Errorf("user: register reset_password: %w", err)
	}

	// Group management
	if err := ctx.Register("user.group_list", a.handleListGroups, actor.Public()); err != nil {
		return fmt.Errorf("user: register group.list: %w", err)
	}
	if err := ctx.Register("user.group_create", a.handleCreateGroup, actor.Public()); err != nil {
		return fmt.Errorf("user: register group.create: %w", err)
	}
	if err := ctx.Register("user.group_update", a.handleUpdateGroup, actor.Public()); err != nil {
		return fmt.Errorf("user: register group.update: %w", err)
	}
	if err := ctx.Register("user.group_remove", a.handleRemoveGroup, actor.Public()); err != nil {
		return fmt.Errorf("user: register group.remove: %w", err)
	}

	// Permission management
	if err := ctx.Register("user.permission_get", a.handleGetPermissions, actor.Public()); err != nil {
		return fmt.Errorf("user: register permission.get: %w", err)
	}
	if err := ctx.Register("user.permission_update", a.handleUpdatePermissions, actor.Public()); err != nil {
		return fmt.Errorf("user: register permission.update: %w", err)
	}

	// Service exposure
	if err := ctx.RegisterDomain("user").Expose(); err != nil {
		return fmt.Errorf("user: expose service: %w", err)
	}

	// Publish the in-process desktop-token issuer. Reachable only via the
	// resource registry (no callable, no transport) — the Wails desktop
	// binary uses this to auto-login without a password.
	if err := resource.Set(ctx.Resources(), DesktopTokenIssuerKey, DesktopTokenIssuer(a.IssueDesktopToken)); err != nil {
		return fmt.Errorf("user: publish desktop token issuer: %w", err)
	}

	return nil
}

// OnStop persists state.
func (a *Actor) OnStop(ctx actor.Context) error {
	return a.Save()
}

// Save persists the actor's state. Caller must NOT hold a.mu.
// Handlers that already hold the lock should call saveLocked via saveOrLog.
func (a *Actor) Save() error {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.saveLocked()
}

// saveLocked persists the actor's state. Caller MUST hold a.mu (read or write).
// Used by handlers that have already taken the write lock to mutate state and
// must not re-enter the lock (Go RWMutex deadlocks on write→read upgrade).
func (a *Actor) saveLocked() error {
	return a.store.Save(a.actorID, map[string]any{
		"accounts":      a.Accounts,
		"refreshTokens": a.RefreshTokens,
		"groups":        a.Groups,
		"permissions":   a.Permissions,
	})
}

// legacyAccount mirrors the old camelCase JSON format for migration.
type legacyAccount struct {
	ID           string   `json:"id"`
	Username     string   `json:"username"`
	DisplayName  string   `json:"displayName"`
	PasswordHash string   `json:"passwordHash"`
	Roles        []string `json:"roles"`
	Groups       []string `json:"groups"`
	Status       string   `json:"status"`
	CreatedAt    string   `json:"createdAt"`
}

// legacyGroup mirrors the old camelCase JSON format for migration.
type legacyGroup struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Roles       []string `json:"roles"`
	MemberCount int      `json:"memberCount"`
}

// legacyPermissionEntry mirrors the old camelCase JSON format for migration.
type legacyPermissionEntry struct {
	Role    string          `json:"role"`
	Actions map[string]bool `json:"actions"`
}

// legacyPermissionMatrix mirrors the old camelCase JSON format for migration.
type legacyPermissionMatrix struct {
	Entries []legacyPermissionEntry `json:"entries"`
}

func convertLegacyAccounts(src []legacyAccount) []gen.Account {
	dst := make([]gen.Account, len(src))
	for i, a := range src {
		dst[i] = gen.Account{
			ID:           a.ID,
			Username:     a.Username,
			DisplayName:  a.DisplayName,
			PasswordHash: a.PasswordHash,
			Roles:        a.Roles,
			Groups:       a.Groups,
			Status:       a.Status,
			CreatedAt:    a.CreatedAt,
		}
	}
	return dst
}

func convertLegacyGroups(src []legacyGroup) []gen.Group {
	dst := make([]gen.Group, len(src))
	for i, g := range src {
		dst[i] = gen.Group{
			ID:          g.ID,
			Name:        g.Name,
			Description: g.Description,
			Roles:       g.Roles,
			MemberCount: int32(g.MemberCount),
		}
	}
	return dst
}

func convertLegacyPermissionMatrix(src legacyPermissionMatrix) gen.PermissionMatrix {
	dst := make([]gen.PermissionEntry, len(src.Entries))
	for i, e := range src.Entries {
		dst[i] = gen.PermissionEntry{
			Role:    e.Role,
			Actions: e.Actions,
		}
	}
	return gen.PermissionMatrix{Entries: dst}
}

// Load restores persisted state. Handles both new PascalCase and legacy camelCase formats.
func (a *Actor) Load() error {
	var snap struct {
		Accounts      []gen.Account           `json:"accounts"`
		RefreshTokens []gen.RefreshTokenEntry `json:"refreshTokens"`
		Groups        []gen.Group             `json:"groups"`
		Permissions   gen.PermissionMatrix    `json:"permissions"`
	}
	_ = persist.LoadOrZero(a.store, a.actorID, &snap)

	// Detect whether new-format data is present.
	hasNewData := false
	for _, acc := range snap.Accounts {
		if acc.ID != "" {
			hasNewData = true
			break
		}
	}
	if !hasNewData && len(snap.Groups) > 0 && snap.Groups[0].ID != "" {
		hasNewData = true
	}
	if !hasNewData && len(snap.Permissions.Entries) > 0 && snap.Permissions.Entries[0].Role != "" {
		hasNewData = true
	}

	if hasNewData {
		a.Accounts = snap.Accounts
		a.RefreshTokens = snap.RefreshTokens
		a.Groups = snap.Groups
		a.Permissions = snap.Permissions
		return nil
	}

	// Try legacy camelCase format.
	var legacy struct {
		Accounts    []legacyAccount        `json:"accounts"`
		Groups      []legacyGroup          `json:"groups"`
		Permissions legacyPermissionMatrix `json:"permissions"`
	}
	if err := persist.LoadOrZero(a.store, a.actorID, &legacy); err != nil {
		return err
	}
	if len(legacy.Accounts) > 0 || len(legacy.Groups) > 0 || len(legacy.Permissions.Entries) > 0 {
		a.Accounts = convertLegacyAccounts(legacy.Accounts)
		a.Groups = convertLegacyGroups(legacy.Groups)
		a.Permissions = convertLegacyPermissionMatrix(legacy.Permissions)
		// Rewrite in new format so next load skips legacy path.
		return a.Save()
	}
	return nil
}

// seedDefaults creates the default admin account and permission matrix if empty.
func (a *Actor) seedDefaults(ctx actor.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()

	changed := false

	// Ensure at least one admin account exists.
	if len(a.Accounts) == 0 {
		hash, err := auth.HashPassword("admin")
		if err != nil {
			ctx.Logger().Error("user: failed to hash default admin password", "error", err)
		} else {
			a.Accounts = append(a.Accounts, gen.Account{
				ID:           "admin",
				Username:     "admin",
				DisplayName:  "Administrator",
				PasswordHash: hash,
				Roles:        []string{"admin"},
				Groups:       []string{"admins"},
				Status:       "active",
				CreatedAt:    time.Now().UTC().Format(time.RFC3339),
			})
			changed = true
			ctx.Logger().Info("user: created default admin account")
		}
	}

	// Seed default groups if empty.
	if len(a.Groups) == 0 {
		a.Groups = []gen.Group{
			{ID: "admins", Name: "admins", Description: "System administrators", Roles: []string{"admin"}},
			{ID: "developers", Name: "developers", Description: "Project developers", Roles: []string{"developer"}},
			{ID: "viewers", Name: "viewers", Description: "Read-only users", Roles: []string{"viewer"}},
		}
		changed = true
	}

	// Seed default permission matrix if empty.
	if len(a.Permissions.Entries) == 0 {
		allActions := []string{
			"workspace.mount",
			"workspace.unmount",
			"workspace.create",
			"workspace.create_agent",
			"workspace.save_agent_kind_config",
			"agent.invoke",
			"project.write",
			"project.shell_exec",
		}
		roles := []string{"admin", "developer", "viewer", "operator"}
		for _, role := range roles {
			actions := make(map[string]bool)
			for _, act := range allActions {
				granted := false
				switch role {
				case "admin":
					granted = true
				case "developer":
					granted = act != "workspace.mount" && act != "workspace.unmount"
				case "operator":
					granted = act == "agent.invoke" || act == "project.shell_exec"
				}
				actions[act] = granted
			}
			a.Permissions.Entries = append(a.Permissions.Entries, gen.PermissionEntry{
				Role:    role,
				Actions: actions,
			})
		}
		changed = true
	}

	if changed {
		if err := a.store.Save(a.actorID, map[string]any{
			"accounts":    a.Accounts,
			"groups":      a.Groups,
			"permissions": a.Permissions,
		}); err != nil {
			ctx.Logger().Error("user: save defaults failed", "error", err)
		}
	}
}

func (a *Actor) saveOrLog(ctx actor.PureContext) {
	// Handlers calling this hold a.mu (write-locked). Use saveLocked so we
	// don't re-enter the lock — sync.RWMutex deadlocks when the same
	// goroutine that holds the write lock tries to take the read lock.
	if err := a.saveLocked(); err != nil {
		ctx.Logger().Error("user: save state failed", "error", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (a *Actor) verifyAdminToken(token string) (auth.Claims, error) {
	claims, err := a.jwt.Verify(token)
	if err != nil {
		return auth.Claims{}, err
	}
	isAdmin := false
	for _, r := range claims.Roles {
		if r == "admin" {
			isAdmin = true
			break
		}
	}
	if !isAdmin {
		return auth.Claims{}, fmt.Errorf("user: admin role required")
	}
	return claims, nil
}

func (a *Actor) verifyAnyToken(token string) (auth.Claims, error) {
	return a.jwt.Verify(token)
}

// identity helpers — auth now comes from ctx.Identity() (gateway layer).

func requireSubject(ctx actor.PureContext) (string, error) {
	ident := ctx.Identity()
	if ident.Subject == "" {
		return "", fmt.Errorf("user: not authenticated")
	}
	return ident.Subject, nil
}

func requireAdmin(ctx actor.PureContext) error {
	if ctx.Identity().Role != "admin" {
		return fmt.Errorf("user: admin role required")
	}
	return nil
}

func requireAuth(ctx actor.PureContext) error {
	if _, err := requireSubject(ctx); err != nil {
		return err
	}
	return nil
}

func toAccountView(acc gen.Account) gen.AccountView {
	return gen.AccountView{
		ID:          acc.ID,
		Username:    acc.Username,
		DisplayName: acc.DisplayName,
		Roles:       append([]string(nil), acc.Roles...),
		Groups:      append([]string(nil), acc.Groups...),
		Status:      acc.Status,
		CreatedAt:   acc.CreatedAt,
	}
}

func validateUsername(u string) error {
	u = strings.TrimSpace(u)
	if u == "" {
		return fmt.Errorf("username is empty")
	}
	if len(u) < 2 || len(u) > 32 {
		return fmt.Errorf("username must be 2-32 characters")
	}
	for _, c := range u {
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-') {
			return fmt.Errorf("username contains invalid character")
		}
	}
	return nil
}

func validatePassword(p string) error {
	if len(p) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Handlers: Auth
// ---------------------------------------------------------------------------

func (a *Actor) handleRegister(ctx actor.PureContext, req gen.AuthRegisterReq) (gen.AccountView, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := validateUsername(req.Username); err != nil {
		return gen.AccountView{}, fmt.Errorf("user.auth.register: %w", err)
	}
	if err := validatePassword(req.Password); err != nil {
		return gen.AccountView{}, fmt.Errorf("user.auth.register: %w", err)
	}

	for _, acc := range a.Accounts {
		if acc.Username == req.Username {
			return gen.AccountView{}, fmt.Errorf("user.auth.register: username %q already exists", req.Username)
		}
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return gen.AccountView{}, fmt.Errorf("user.auth.register: %w", err)
	}

	// Self-registration always defaults to viewer; only admins can assign roles.
	roles := []string{"viewer"}

	acc := gen.Account{
		ID:           fmt.Sprintf("u%d", time.Now().UnixNano()),
		Username:     req.Username,
		DisplayName:  strings.TrimSpace(req.DisplayName),
		PasswordHash: hash,
		Roles:        roles,
		Groups:       []string{},
		Status:       "active",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	a.Accounts = append(a.Accounts, acc)
	a.saveOrLog(ctx)

	ctx.Logger().Info("user: registered", "username", acc.Username, "id", acc.ID)
	return toAccountView(acc), nil
}

func (a *Actor) handleLogin(ctx actor.PureContext, req gen.AuthLoginReq) (gen.AuthLoginResp, error) {
	// Find account under read lock, copy out what we need, then release.
	a.mu.RLock()
	var found *gen.Account
	for i := range a.Accounts {
		if a.Accounts[i].Username == req.Username {
			found = &a.Accounts[i]
			break
		}
	}
	if found == nil {
		a.mu.RUnlock()
		return gen.AuthLoginResp{}, fmt.Errorf("user.auth.login: invalid username or password")
	}
	if found.Status != "active" {
		a.mu.RUnlock()
		return gen.AuthLoginResp{}, fmt.Errorf("user.auth.login: account is disabled")
	}
	if !auth.CheckPassword(req.Password, found.PasswordHash) {
		a.mu.RUnlock()
		return gen.AuthLoginResp{}, fmt.Errorf("user.auth.login: invalid username or password")
	}

	account := *found
	a.mu.RUnlock()

	pair, err := a.jwt.Issue(account.ID, account.Username, account.Roles)
	if err != nil {
		return gen.AuthLoginResp{}, fmt.Errorf("user.auth.login: %w", err)
	}

	refreshToken := a.jwt.IssueRefreshToken()
	refreshExpiry := time.Now().Add(a.jwt.RefreshExpiryTime())
	a.mu.Lock()
	a.RefreshTokens = append(a.RefreshTokens, gen.RefreshTokenEntry{
		Token:     refreshToken,
		UserID:    account.ID,
		ExpiresAt: refreshExpiry.Format(time.RFC3339),
	})
	a.mu.Unlock()
	if err := a.Save(); err != nil {
		ctx.Logger().Error("user: save refresh token failed", "error", err)
	}

	ctx.Logger().Info("user: login", "username", account.Username)
	return gen.AuthLoginResp{
		Token:        pair.AccessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    pair.ExpiresAt.Format(time.RFC3339),
		Account:      toAccountView(account),
	}, nil
}

func (a *Actor) handleMe(ctx actor.PureContext) (gen.AccountView, error) {
	uid, err := requireSubject(ctx)
	if err != nil {
		return gen.AccountView{}, fmt.Errorf("user.auth.me: %w", err)
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, acc := range a.Accounts {
		if acc.ID == uid {
			return toAccountView(acc), nil
		}
	}
	return gen.AccountView{}, fmt.Errorf("user.auth.me: account not found")
}

func (a *Actor) handleRefresh(ctx actor.PureContext, req gen.AuthRefreshReq) (gen.AuthRefreshResp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	idx := -1
	for i, rt := range a.RefreshTokens {
		if rt.Token == req.RefreshToken {
			idx = i
			break
		}
	}
	if idx < 0 {
		return gen.AuthRefreshResp{}, fmt.Errorf("user.auth.refresh: invalid refresh token")
	}

	entry := a.RefreshTokens[idx]
	expiresAt, err := time.Parse(time.RFC3339, entry.ExpiresAt)
	if err != nil || time.Now().After(expiresAt) {
		a.RefreshTokens = append(a.RefreshTokens[:idx], a.RefreshTokens[idx+1:]...)
		_ = a.saveLocked()
		return gen.AuthRefreshResp{}, fmt.Errorf("user.auth.refresh: refresh token expired")
	}

	var acc *gen.Account
	for i := range a.Accounts {
		if a.Accounts[i].ID == entry.UserID {
			acc = &a.Accounts[i]
			break
		}
	}
	if acc == nil {
		a.RefreshTokens = append(a.RefreshTokens[:idx], a.RefreshTokens[idx+1:]...)
		_ = a.saveLocked()
		return gen.AuthRefreshResp{}, fmt.Errorf("user.auth.refresh: account not found")
	}
	if acc.Status != "active" {
		return gen.AuthRefreshResp{}, fmt.Errorf("user.auth.refresh: account is disabled")
	}

	// Rotate: delete old refresh token, issue new pair.
	a.RefreshTokens = append(a.RefreshTokens[:idx], a.RefreshTokens[idx+1:]...)
	pair, err := a.jwt.Issue(acc.ID, acc.Username, acc.Roles)
	if err != nil {
		return gen.AuthRefreshResp{}, fmt.Errorf("user.auth.refresh: %w", err)
	}
	newRefresh := a.jwt.IssueRefreshToken()
	refreshExpiry := time.Now().Add(a.jwt.RefreshExpiryTime())
	a.RefreshTokens = append(a.RefreshTokens, gen.RefreshTokenEntry{
		Token:     newRefresh,
		UserID:    acc.ID,
		ExpiresAt: refreshExpiry.Format(time.RFC3339),
	})
	if err := a.saveLocked(); err != nil {
		ctx.Logger().Error("user: save after refresh rotation failed", "error", err)
	}

	return gen.AuthRefreshResp{
		Token:        pair.AccessToken,
		RefreshToken: newRefresh,
		ExpiresAt:    pair.ExpiresAt.Format(time.RFC3339),
	}, nil
}

func (a *Actor) cleanupExpiredRefreshTokens() {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	filtered := a.RefreshTokens[:0]
	for _, rt := range a.RefreshTokens {
		expiresAt, err := time.Parse(time.RFC3339, rt.ExpiresAt)
		if err != nil || now.After(expiresAt) {
			continue
		}
		filtered = append(filtered, rt)
	}
	a.RefreshTokens = filtered
}

// IssueDesktopToken issues a JWT for the local admin account without
// requiring a password. Intended for in-process callers that already have
// process-level trust (the Wails desktop binary lives in the same address
// space as the actor and would already need root access to read secrets).
//
// The account status is deliberately ignored: disabling the admin account
// must only lock out remote web logins (auth.login / auth.refresh), never
// the local desktop client.
//
// Not registered as a callable: this is a Go method called directly via a
// service lookup, so it never crosses the WebSocket / HTTP boundary and
// cannot be reached by remote clients. The trust model is "if you can call
// this, you're already inside the process."
func (a *Actor) IssueDesktopToken() (string, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()

	for _, acc := range a.Accounts {
		if acc.Username != "admin" {
			continue
		}
		pair, err := a.jwt.Issue(acc.ID, acc.Username, acc.Roles)
		if err != nil {
			return "", fmt.Errorf("user.IssueDesktopToken: %w", err)
		}
		return pair.AccessToken, nil
	}
	return "", fmt.Errorf("user.IssueDesktopToken: admin account not found")
}

// ---------------------------------------------------------------------------
// Handlers: Accounts
// ---------------------------------------------------------------------------

func (a *Actor) handleListAccounts(ctx actor.PureContext) (gen.AccountListResp, error) {
	if err := requireAuth(ctx); err != nil {
		return gen.AccountListResp{}, fmt.Errorf("user.list: %w", err)
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	items := make([]gen.AccountView, len(a.Accounts))
	for i, acc := range a.Accounts {
		items[i] = toAccountView(acc)
	}
	return gen.AccountListResp{Items: items}, nil
}

func (a *Actor) handleCreateAccount(ctx actor.PureContext, req gen.AccountCreateReq) (gen.AccountView, error) {
	if err := requireAdmin(ctx); err != nil {
		return gen.AccountView{}, fmt.Errorf("user.create: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if err := validateUsername(req.Username); err != nil {
		return gen.AccountView{}, fmt.Errorf("user.create: %w", err)
	}
	if err := validatePassword(req.Password); err != nil {
		return gen.AccountView{}, fmt.Errorf("user.create: %w", err)
	}

	for _, acc := range a.Accounts {
		if acc.Username == req.Username {
			return gen.AccountView{}, fmt.Errorf("user.create: username %q already exists", req.Username)
		}
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		return gen.AccountView{}, fmt.Errorf("user.create: %w", err)
	}

	roles := req.Roles
	if len(roles) == 0 {
		roles = []string{"viewer"}
	}

	acc := gen.Account{
		ID:           fmt.Sprintf("u%d", time.Now().UnixNano()),
		Username:     req.Username,
		DisplayName:  strings.TrimSpace(req.DisplayName),
		PasswordHash: hash,
		Roles:        roles,
		Groups:       req.Groups,
		Status:       "active",
		CreatedAt:    time.Now().UTC().Format(time.RFC3339),
	}
	a.Accounts = append(a.Accounts, acc)
	a.saveOrLog(ctx)
	return toAccountView(acc), nil
}

func (a *Actor) handleUpdateAccount(ctx actor.PureContext, req gen.AccountUpdateReq) (gen.AccountView, error) {
	uid, err := requireSubject(ctx)
	if err != nil {
		return gen.AccountView{}, fmt.Errorf("user.update: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	isAdmin := ctx.Identity().Role == "admin"
	if !isAdmin && uid != req.ID {
		return gen.AccountView{}, fmt.Errorf("user.update: cannot update other users")
	}

	for i := range a.Accounts {
		if a.Accounts[i].ID != req.ID {
			continue
		}
		if err := guardBuiltinAdmin(a.Accounts[i], req); err != nil {
			return gen.AccountView{}, fmt.Errorf("user.update: %w", err)
		}
		if req.DisplayName != "" {
			a.Accounts[i].DisplayName = req.DisplayName
		}
		if isAdmin && len(req.Roles) > 0 {
			a.Accounts[i].Roles = req.Roles
		}
		if isAdmin && len(req.Groups) > 0 {
			a.Accounts[i].Groups = req.Groups
		}
		if req.Status != "" && isAdmin {
			a.Accounts[i].Status = req.Status
		}
		a.saveOrLog(ctx)
		return toAccountView(a.Accounts[i]), nil
	}
	return gen.AccountView{}, fmt.Errorf("user.update: account %q not found", req.ID)
}

// guardBuiltinAdmin rejects name, role, and group edits on the default admin
// account seeded at startup so its identity and privileges stay stable.
func guardBuiltinAdmin(acc gen.Account, req gen.AccountUpdateReq) error {
	if acc.ID != "admin" || acc.Username != "admin" {
		return nil
	}
	if req.DisplayName != "" && strings.TrimSpace(req.DisplayName) != acc.DisplayName {
		return fmt.Errorf("cannot change name of built-in admin account")
	}
	if len(req.Roles) > 0 && !slices.Equal(req.Roles, acc.Roles) {
		return fmt.Errorf("cannot change roles of built-in admin account")
	}
	if len(req.Groups) > 0 && !slices.Equal(req.Groups, acc.Groups) {
		return fmt.Errorf("cannot change groups of built-in admin account")
	}
	return nil
}

func (a *Actor) handleRemoveAccount(ctx actor.PureContext, req gen.AccountDeleteReq) (gen.AccountView, error) {
	if err := requireAdmin(ctx); err != nil {
		return gen.AccountView{}, fmt.Errorf("user.remove: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i, acc := range a.Accounts {
		if acc.ID != req.ID {
			continue
		}
		if acc.Username == "admin" {
			return gen.AccountView{}, fmt.Errorf("user.remove: cannot delete default admin")
		}
		deleted := toAccountView(acc)
		a.Accounts = append(a.Accounts[:i], a.Accounts[i+1:]...)
		a.saveOrLog(ctx)
		return deleted, nil
	}
	return gen.AccountView{}, fmt.Errorf("user.remove: account %q not found", req.ID)
}

func (a *Actor) handleResetPassword(ctx actor.PureContext, req gen.AccountResetPasswordReq) (gen.AccountView, error) {
	uid, err := requireSubject(ctx)
	if err != nil {
		return gen.AccountView{}, fmt.Errorf("user.reset_password: %w", err)
	}

	isAdmin := ctx.Identity().Role == "admin"
	if !isAdmin && uid != req.ID {
		return gen.AccountView{}, fmt.Errorf("user.reset_password: cannot reset other user's password")
	}

	if err := validatePassword(req.Password); err != nil {
		return gen.AccountView{}, fmt.Errorf("user.reset_password: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.Accounts {
		if a.Accounts[i].ID != req.ID {
			continue
		}
		hash, err := auth.HashPassword(req.Password)
		if err != nil {
			return gen.AccountView{}, fmt.Errorf("user.reset_password: %w", err)
		}
		a.Accounts[i].PasswordHash = hash
		a.saveOrLog(ctx)
		return toAccountView(a.Accounts[i]), nil
	}
	return gen.AccountView{}, fmt.Errorf("user.reset_password: account %q not found", req.ID)
}

// ---------------------------------------------------------------------------
// Handlers: Groups
// ---------------------------------------------------------------------------

func (a *Actor) handleListGroups(ctx actor.PureContext) (gen.GroupListResp, error) {
	if err := requireAuth(ctx); err != nil {
		return gen.GroupListResp{}, fmt.Errorf("user.group.list: %w", err)
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	items := make([]gen.Group, len(a.Groups))
	copy(items, a.Groups)
	// Recalculate member counts.
	for i := range items {
		count := 0
		for _, acc := range a.Accounts {
			for _, g := range acc.Groups {
				if g == items[i].Name {
					count++
					break
				}
			}
		}
		items[i].MemberCount = int32(count)
	}
	return gen.GroupListResp{Items: items}, nil
}

func (a *Actor) handleCreateGroup(ctx actor.PureContext, req gen.GroupCreateReq) (gen.Group, error) {
	if err := requireAdmin(ctx); err != nil {
		return gen.Group{}, fmt.Errorf("user.group.create: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	name := strings.TrimSpace(req.Name)
	if name == "" {
		return gen.Group{}, fmt.Errorf("user.group.create: name is empty")
	}
	for _, g := range a.Groups {
		if g.Name == name {
			return gen.Group{}, fmt.Errorf("user.group.create: group %q already exists", name)
		}
	}

	group := gen.Group{
		ID:          fmt.Sprintf("g%d", time.Now().UnixNano()),
		Name:        name,
		Description: strings.TrimSpace(req.Description),
		Roles:       req.Roles,
	}
	a.Groups = append(a.Groups, group)
	a.saveOrLog(ctx)
	return group, nil
}

func (a *Actor) handleUpdateGroup(ctx actor.PureContext, req gen.GroupUpdateReq) (gen.Group, error) {
	if err := requireAdmin(ctx); err != nil {
		return gen.Group{}, fmt.Errorf("user.group.update: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i := range a.Groups {
		if a.Groups[i].ID != req.ID {
			continue
		}
		if req.Name != "" {
			a.Groups[i].Name = req.Name
		}
		if req.Description != "" {
			a.Groups[i].Description = req.Description
		}
		if len(req.Roles) > 0 {
			a.Groups[i].Roles = req.Roles
		}
		a.saveOrLog(ctx)
		return a.Groups[i], nil
	}
	return gen.Group{}, fmt.Errorf("user.group.update: group %q not found", req.ID)
}

func (a *Actor) handleRemoveGroup(ctx actor.PureContext, req gen.GroupDeleteReq) (gen.Group, error) {
	if err := requireAdmin(ctx); err != nil {
		return gen.Group{}, fmt.Errorf("user.group.remove: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	for i, g := range a.Groups {
		if g.ID != req.ID {
			continue
		}
		deleted := g
		a.Groups = append(a.Groups[:i], a.Groups[i+1:]...)
		// Remove group from all accounts.
		for j := range a.Accounts {
			var ng []string
			for _, grp := range a.Accounts[j].Groups {
				if grp != deleted.Name {
					ng = append(ng, grp)
				}
			}
			a.Accounts[j].Groups = ng
		}
		a.saveOrLog(ctx)
		return deleted, nil
	}
	return gen.Group{}, fmt.Errorf("user.group.remove: group %q not found", req.ID)
}

// ---------------------------------------------------------------------------
// Handlers: Permissions
// ---------------------------------------------------------------------------

func (a *Actor) handleGetPermissions(ctx actor.PureContext) (gen.PermissionMatrix, error) {
	if err := requireAuth(ctx); err != nil {
		return gen.PermissionMatrix{}, fmt.Errorf("user.permission.get: %w", err)
	}

	a.mu.RLock()
	defer a.mu.RUnlock()

	// Return a copy.
	entries := make([]gen.PermissionEntry, len(a.Permissions.Entries))
	for i, e := range a.Permissions.Entries {
		actions := make(map[string]bool, len(e.Actions))
		for k, v := range e.Actions {
			actions[k] = v
		}
		entries[i] = gen.PermissionEntry{Role: e.Role, Actions: actions}
	}
	return gen.PermissionMatrix{Entries: entries}, nil
}

func (a *Actor) handleUpdatePermissions(ctx actor.PureContext, req gen.PermissionUpdateReq) (gen.PermissionMatrix, error) {
	if err := requireAdmin(ctx); err != nil {
		return gen.PermissionMatrix{}, fmt.Errorf("user.permission.update: %w", err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	found := false
	for i := range a.Permissions.Entries {
		if a.Permissions.Entries[i].Role != req.Role {
			continue
		}
		a.Permissions.Entries[i].Actions = req.Actions
		found = true
		break
	}
	if !found {
		a.Permissions.Entries = append(a.Permissions.Entries, gen.PermissionEntry{
			Role:    req.Role,
			Actions: req.Actions,
		})
	}
	a.saveOrLog(ctx)
	return a.handleGetPermissions(ctx)
}
