package im

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/qomos-w/gospore/actor"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

// newAccountID returns a short random id, e.g. "ia_1a2b3c4d".
func newAccountID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "ia_" + hex.EncodeToString(b[:])
}

// accountView projects a stored account into its redacted ImAccountView
// shape: the token never crosses the actor boundary, only HasToken.
func accountView(acc gen.ImAccount) gen.ImAccountView {
	return gen.ImAccountView{
		ID: acc.ID, Name: acc.Name, Provider: acc.Provider, Enabled: acc.Enabled,
		HasToken: acc.Token != "", AllowUsers: acc.AllowUsers, APIBase: acc.APIBase,
	}
}

// findAccount returns the index of the account with the given id, or -1.
func findAccount(st imStore, id string) int {
	for i := range st.Accounts {
		if st.Accounts[i].ID == id {
			return i
		}
	}
	return -1
}

func (a *Actor) handleAccountList(_ actor.PureContext, _ gen.ImAccountListReq) (gen.ImAccountListResp, error) {
	st, err := a.loadStore()
	if err != nil {
		return gen.ImAccountListResp{}, fmt.Errorf("im.account.list: load accounts: %w", err)
	}
	items := make([]gen.ImAccountView, 0, len(st.Accounts))
	for i := range st.Accounts {
		items = append(items, accountView(st.Accounts[i]))
	}
	return gen.ImAccountListResp{Items: items}, nil
}

func (a *Actor) handleAccountCreate(_ actor.PureContext, req gen.ImAccountCreateReq) (gen.ImAccountCreateResp, error) {
	if req.Name == "" || req.Provider == "" || req.Token == "" {
		return gen.ImAccountCreateResp{}, fmt.Errorf("im.account.create: name, provider and token are required")
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.ImAccountCreateResp{}, fmt.Errorf("im.account.create: load accounts: %w", err)
	}
	acc := gen.ImAccount{
		ID: newAccountID(),
		Name:     req.Name,
		Provider: req.Provider,
		Token:    req.Token,
		// The schema's optional bool cannot express false on the wire
		// (omitempty), so per the contract comment Enabled defaults to true.
		Enabled:    true,
		AllowUsers: req.AllowUsers,
		APIBase:    req.APIBase,
	}
	st.Accounts = append(st.Accounts, acc)
	if err := a.saveStore(st); err != nil {
		return gen.ImAccountCreateResp{}, fmt.Errorf("im.account.create: save accounts: %w", err)
	}
	return gen.ImAccountCreateResp{Account: accountView(acc)}, nil
}

func (a *Actor) handleAccountUpdate(_ actor.PureContext, req gen.ImAccountUpdateReq) (gen.ImAccountUpdateResp, error) {
	if req.ID == "" {
		return gen.ImAccountUpdateResp{}, fmt.Errorf("im.account.update: account id is required")
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.ImAccountUpdateResp{}, fmt.Errorf("im.account.update: load accounts: %w", err)
	}
	idx := findAccount(st, req.ID)
	if idx < 0 {
		return gen.ImAccountUpdateResp{}, fmt.Errorf("im.account.update: account %q not found", req.ID)
	}
	acc := st.Accounts[idx]
	if req.Name != "" {
		acc.Name = req.Name
	}
	// Empty Token preserves the stored secret (redacted edit flow).
	if req.Token != "" {
		acc.Token = req.Token
	}
	// Optional bool cannot express false on the wire, so Enabled can only be
	// turned on here; disabling requires a schema-level fix.
	if req.Enabled {
		acc.Enabled = true
	}
	// nil preserves the stored list; an empty list cannot be expressed on the
	// wire (omitempty drops empty slices) and needs the same schema fix.
	if req.AllowUsers != nil {
		acc.AllowUsers = req.AllowUsers
	}
	if req.APIBase != "" {
		acc.APIBase = req.APIBase
	}
	st.Accounts[idx] = acc
	if err := a.saveStore(st); err != nil {
		return gen.ImAccountUpdateResp{}, fmt.Errorf("im.account.update: save accounts: %w", err)
	}
	return gen.ImAccountUpdateResp{Account: accountView(acc)}, nil
}

func (a *Actor) handleAccountDelete(_ actor.PureContext, req gen.ImAccountDeleteReq) (gen.ImAccountDeleteResp, error) {
	if req.ID == "" {
		return gen.ImAccountDeleteResp{}, fmt.Errorf("im.account.delete: account id is required")
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.ImAccountDeleteResp{}, fmt.Errorf("im.account.delete: load accounts: %w", err)
	}
	idx := findAccount(st, req.ID)
	if idx < 0 {
		return gen.ImAccountDeleteResp{}, fmt.Errorf("im.account.delete: account %q not found", req.ID)
	}
	st.Accounts = append(st.Accounts[:idx], st.Accounts[idx+1:]...)
	if err := a.saveStore(st); err != nil {
		return gen.ImAccountDeleteResp{}, fmt.Errorf("im.account.delete: save accounts: %w", err)
	}
	return gen.ImAccountDeleteResp{}, nil
}
