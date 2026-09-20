// handlers.go — created by generator; agent-owned afterwards.
// New callables get stubs appended by regenerate; existing functions are never overwritten.
package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"net/url"
	"strings"
	"sync"
	"time"

	sdk "github.com/qomos-w/sporemind-plugin-sdk"
)

// stateKey is the single per-app state slot holding the account list as JSON.
const stateKey = "accounts"

// account is one persisted TOTP account.
type account struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	SecretB32 string `json:"secret_b32"`
	Digits    int32  `json:"digits"`
	Period    int32  `json:"period"`
	Algorithm string `json:"algorithm"`
	Created   int64  `json:"created_unix"`
}

// stateMu guards read-modify-write cycles over the shared state slot:
// concurrent plugin invokes can race otherwise.
var stateMu sync.Mutex

func loadAccounts() ([]account, error) {
	resp, err := CallStateGet(sdk.ActiveHost(), StateKeyReq{Key: stateKey})
	if err != nil {
		return nil, fmt.Errorf("state.get: %w", err)
	}
	if !resp.Found || len(resp.Value) == 0 {
		return nil, nil
	}
	var accs []account
	if err := json.Unmarshal(resp.Value, &accs); err != nil {
		return nil, fmt.Errorf("decode accounts: %w", err)
	}
	return accs, nil
}

func saveAccounts(accs []account) error {
	raw, err := json.Marshal(accs)
	if err != nil {
		return err
	}
	_, err = CallStateSet(sdk.ActiveHost(), StateSetReq{Key: stateKey, Value: raw})
	return err
}

// --- RFC 4226 / 6238 core ---

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newAlgorithmFunc(name string) (func() hash.Hash, error) {
	switch strings.ToUpper(name) {
	case "SHA1", "":
		return sha1.New, nil
	case "SHA256":
		return sha256.New, nil
	case "SHA512":
		return sha512.New, nil
	}
	return nil, fmt.Errorf("unsupported algorithm %q", name)
}

// hotp computes the RFC 4226 HOTP value for a raw secret and counter.
func hotp(secret []byte, counter uint64, digits int32, algo string) (string, error) {
	newHash, err := newAlgorithmFunc(algo)
	if err != nil {
		return "", err
	}
	mac := hmac.New(newHash, secret)
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], counter)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	bin := (uint32(sum[offset])&0x7f)<<24 |
		uint32(sum[offset+1])<<16 |
		uint32(sum[offset+2])<<8 |
		uint32(sum[offset+3])
	mod := uint32(1)
	for i := int32(0); i < digits; i++ {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", digits, bin%mod), nil
}

// totpAt computes the TOTP code for a unix timestamp.
func totpAt(secret []byte, t time.Time, digits, period int32, algo string) (string, error) {
	if period <= 0 {
		period = 30
	}
	return hotp(secret, uint64(t.Unix()/int64(period)), digits, algo)
}

// --- callables ---

func handleProvision(req sdk.Request) (sdk.Response, error) {
	var payload ProvisionRequest
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return sdk.Response{}, err
	}
	name := strings.TrimSpace(payload.Name)
	if name == "" {
		return sdk.Response{}, fmt.Errorf("Name is required")
	}

	digits := int32(6)
	if payload.Digits != nil {
		switch *payload.Digits {
		case 6, 8:
			digits = *payload.Digits
		default:
			return sdk.Response{}, fmt.Errorf("Digits must be 6 or 8")
		}
	}
	period := int32(30)
	if payload.Period != nil && *payload.Period >= 5 && *payload.Period <= 300 {
		period = *payload.Period
	}
	algorithm := "SHA1"
	if payload.Algorithm != nil && *payload.Algorithm != "" {
		algorithm = strings.ToUpper(*payload.Algorithm)
	}
	if _, err := newAlgorithmFunc(algorithm); err != nil {
		return sdk.Response{}, err
	}

	var secret []byte
	secretB32 := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(deref(payload.Secret)), " ", ""))
	if secretB32 != "" {
		decoded, err := b32.DecodeString(secretB32)
		if err != nil {
			return sdk.Response{}, fmt.Errorf("Secret is not valid base32: %w", err)
		}
		if len(decoded) < 10 {
			return sdk.Response{}, fmt.Errorf("Secret too short: need >= 10 bytes")
		}
		secret = decoded
		secretB32 = b32.EncodeToString(decoded)
	} else {
		secret = make([]byte, 20)
		if _, err := rand.Read(secret); err != nil {
			return sdk.Response{}, err
		}
		secretB32 = b32.EncodeToString(secret)
	}

	acc := account{
		ID:        "acc_" + randomHex(6),
		Name:      name,
		SecretB32: secretB32,
		Digits:    digits,
		Period:    period,
		Algorithm: algorithm,
		Created:   time.Now().Unix(),
	}

	stateMu.Lock()
	accs, err := loadAccounts()
	if err != nil {
		stateMu.Unlock()
		return sdk.Response{}, err
	}
	accs = append(accs, acc)
	if err := saveAccounts(accs); err != nil {
		stateMu.Unlock()
		return sdk.Response{}, err
	}
	stateMu.Unlock()

	resp := ProvisionResponse{
		AccountId:    acc.ID,
		Name:         acc.Name,
		SecretBase32: acc.SecretB32,
		OtpAuthUrl:   otpAuthURL(acc),
		Digits:       acc.Digits,
		Period:       acc.Period,
		Algorithm:    acc.Algorithm,
	}
	return sdk.Response{Payload: resp}, nil
}

func handleCodes(req sdk.Request) (sdk.Response, error) {
	var payload CodesRequest
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return sdk.Response{}, err
	}

	stateMu.Lock()
	accs, err := loadAccounts()
	stateMu.Unlock()
	if err != nil {
		return sdk.Response{}, err
	}

	now := time.Now()
	out := make([]AccountCode, 0, len(accs))
	for _, acc := range accs {
		if payload.AccountId != nil && *payload.AccountId != "" && acc.ID != *payload.AccountId {
			continue
		}
		secret, err := b32.DecodeString(acc.SecretB32)
		if err != nil {
			return sdk.Response{}, fmt.Errorf("account %s has corrupt secret: %w", acc.ID, err)
		}
		code, err := totpAt(secret, now, acc.Digits, acc.Period, acc.Algorithm)
		if err != nil {
			return sdk.Response{}, err
		}
		period := acc.Period
		if period <= 0 {
			period = 30
		}
		out = append(out, AccountCode{
			AccountId:        acc.ID,
			Name:             acc.Name,
			Code:             code,
			SecretBase32:     acc.SecretB32,
			SecondsRemaining: int32(period - int32(now.Unix()%int64(period))),
			Period:           period,
		})
	}

	return sdk.Response{Payload: CodesResponse{Accounts: out, NowUnix: now.Unix()}}, nil
}

func handleVerify(req sdk.Request) (sdk.Response, error) {
	var payload VerifyRequest
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return sdk.Response{}, err
	}
	code := strings.TrimSpace(payload.Code)
	if payload.AccountId == "" || code == "" {
		return sdk.Response{Payload: VerifyResponse{Valid: false, AccountId: payload.AccountId, Error: "AccountId and Code are required"}}, nil
	}

	window := int32(1)
	if payload.Window != nil && *payload.Window >= 0 && *payload.Window <= 5 {
		window = *payload.Window
	}

	stateMu.Lock()
	accs, err := loadAccounts()
	stateMu.Unlock()
	if err != nil {
		return sdk.Response{}, err
	}

	for _, acc := range accs {
		if acc.ID != payload.AccountId {
			continue
		}
		secret, err := b32.DecodeString(acc.SecretB32)
		if err != nil {
			return sdk.Response{}, fmt.Errorf("account %s has corrupt secret: %w", acc.ID, err)
		}
		period := acc.Period
		if period <= 0 {
			period = 30
		}
		now := time.Now()
		for drift := int64(-int64(window)); drift <= int64(window); drift++ {
			t := now.Add(time.Duration(drift*int64(period)) * time.Second)
			want, err := totpAt(secret, t, acc.Digits, period, acc.Algorithm)
			if err != nil {
				return sdk.Response{}, err
			}
			if hmac.Equal([]byte(want), []byte(code)) {
				return sdk.Response{Payload: VerifyResponse{Valid: true, AccountId: acc.ID}}, nil
			}
		}
		return sdk.Response{Payload: VerifyResponse{Valid: false, AccountId: acc.ID, Error: "code mismatch or expired"}}, nil
	}

	return sdk.Response{Payload: VerifyResponse{Valid: false, AccountId: payload.AccountId, Error: "account not found"}}, nil
}

func handleRemove(req sdk.Request) (sdk.Response, error) {
	var payload RemoveRequest
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return sdk.Response{}, err
	}
	if payload.AccountId == "" {
		return sdk.Response{}, fmt.Errorf("AccountId is required")
	}

	stateMu.Lock()
	defer stateMu.Unlock()
	accs, err := loadAccounts()
	if err != nil {
		return sdk.Response{}, err
	}
	kept := accs[:0]
	removed := false
	for _, acc := range accs {
		if acc.ID == payload.AccountId {
			removed = true
			continue
		}
		kept = append(kept, acc)
	}
	if !removed {
		return sdk.Response{Payload: RemoveResponse{Removed: false, AccountId: payload.AccountId}}, nil
	}
	if err := saveAccounts(kept); err != nil {
		return sdk.Response{}, err
	}
	return sdk.Response{Payload: RemoveResponse{Removed: true, AccountId: payload.AccountId}}, nil
}

// --- helpers ---

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func otpAuthURL(acc account) string {
	label := url.PathEscape("Sporemind:" + acc.Name)
	q := url.Values{}
	q.Set("secret", acc.SecretB32)
	q.Set("issuer", "Sporemind")
	q.Set("algorithm", acc.Algorithm)
	q.Set("digits", fmt.Sprintf("%d", acc.Digits))
	q.Set("period", fmt.Sprintf("%d", acc.Period))
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// TODO(agent): implement update
func handleUpdate(req sdk.Request) (sdk.Response, error) {
	var payload UpdateRequest
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return sdk.Response{}, err
	}
	if payload.AccountId == "" {
		return sdk.Response{}, fmt.Errorf("AccountId is required")
	}
	newName := strings.TrimSpace(deref(payload.Name))
	newSecretRaw := strings.TrimSpace(deref(payload.Secret))
	if newName == "" && newSecretRaw == "" {
		return sdk.Response{Payload: UpdateResponse{Updated: false, AccountId: payload.AccountId}}, nil
	}

	var newSecretB32 string
	if newSecretRaw != "" {
		norm := strings.ToUpper(strings.ReplaceAll(newSecretRaw, " ", ""))
		decoded, err := b32.DecodeString(norm)
		if err != nil {
			return sdk.Response{}, fmt.Errorf("Secret is not valid base32: %w", err)
		}
		if len(decoded) < 10 {
			return sdk.Response{}, fmt.Errorf("Secret too short: need >= 10 bytes")
		}
		newSecretB32 = b32.EncodeToString(decoded)
	}

	stateMu.Lock()
	defer stateMu.Unlock()
	accs, err := loadAccounts()
	if err != nil {
		return sdk.Response{}, err
	}
	for i := range accs {
		if accs[i].ID != payload.AccountId {
			continue
		}
		if newName != "" {
			accs[i].Name = newName
		}
		if newSecretB32 != "" {
			accs[i].SecretB32 = newSecretB32
		}
		if err := saveAccounts(accs); err != nil {
			return sdk.Response{}, err
		}
		acc := accs[i]
		return sdk.Response{Payload: UpdateResponse{
			Updated:      true,
			AccountId:    acc.ID,
			Name:         acc.Name,
			SecretBase32: acc.SecretB32,
			OtpAuthUrl:   otpAuthURL(acc),
		}}, nil
	}
	return sdk.Response{Payload: UpdateResponse{Updated: false, AccountId: payload.AccountId}}, nil
}

// TODO(agent): implement OnAppLifecycle — host event app_lifecycle (fired whenever any app registers, reloads, unloads, or fails; payload is AppLifecycleEvent)
func OnAppLifecycle(ev AppLifecycleEvent) error {
	return sdk.ErrNotImplemented
}

// TODO(agent): implement OnAppEvent — host event app_event (generic envelope for events cast to bound consumers (Id/Event/Payload/Sender); phase 1 delivers the whole class, no per-event filtering)
func OnAppEvent(ev AppEventMessage) error {
	return sdk.ErrNotImplemented
}

// handleDiscover exercises the registry.read capability end-to-end: it calls
// sdk.ListCallables (the SDK wrapper over the registry.query host call), so a
// real plugin process drives the host bridge capability gate, the
// pluginhost's local registry handler, substring filters, and cursor
// pagination. Metadata only — the response carries no credential values.
func handleDiscover(req sdk.Request) (sdk.Response, error) {
	var payload DiscoverRequest
	if err := json.Unmarshal(req.Payload, &payload); err != nil {
		return sdk.Response{}, err
	}

	q := sdk.ListCallablesQuery{
		Service:  deref(payload.Service),
		Callable: deref(payload.Callable),
	}
	if payload.Limit != nil {
		q.Limit = int(*payload.Limit)
	}
	if payload.Cursor != nil {
		q.Cursor = *payload.Cursor
	}

	resp, err := sdk.ListCallables(q)
	if err != nil {
		// A denied/failed host call surfaces here — e.g. "capability not
		// granted (registry.read) for callID registry.query" when the app
		// was loaded without the declared permission.
		return sdk.Response{}, fmt.Errorf("ListCallables: %w", err)
	}

	out := DiscoverResponse{NextCursor: resp.NextCursor}
	for _, m := range resp.Items {
		out.Items = append(out.Items, DiscoverMatch{
			CallID:       m.CallID,
			Service:      m.Service,
			Description:  m.Description,
			Permission:   m.Permission,
			EffectKind:   m.EffectKind,
			ReqSchemaId:  int64(m.ReqSchemaID),
			RespSchemaId: int64(m.RespSchemaID),
		})
	}
	return sdk.Response{Payload: out}, nil
}
