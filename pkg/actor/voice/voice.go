package voice

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/qomos-w/gospore/actor"
	"github.com/qomos-w/sporemind/pkg/config"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"

	"github.com/qomos-w/sporemind/pkg/llmclient"
	"github.com/qomos-w/sporemind/pkg/persist"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func voiceDataDir(sub string) string {
	return filepath.Join(config.ActorDataDir(), "voice", sub)
}

// saveIncomingAudio writes every received audio payload to disk for analysis.
func saveIncomingAudio(audioType int32, rawData []byte) string {
	ts := time.Now().UTC().Format("20060102_150405.000")
	base := fmt.Sprintf("%s_type%d_len%d", ts, audioType, len(rawData))
	dir := voiceDataDir("incoming")
	_ = os.MkdirAll(dir, 0755)
	binPath := filepath.Join(dir, base+".bin")
	_ = os.WriteFile(binPath, rawData, 0644)
	return binPath
}

// analyzeWAV parses the WAV header and logs key fields.
func analyzeWAV(data []byte, logger actor.Logger) {
	if len(data) < 44 {
		logger.Error("voice: wav too short for header", "len", len(data))
		return
	}
	// RIFF header
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		logger.Error("voice: not a valid WAV file", "header", string(data[0:12]))
		return
	}
	// fmt chunk
	if string(data[12:16]) != "fmt " {
		logger.Error("voice: WAV missing fmt chunk", "chunk", string(data[12:16]))
		return
	}
	audioFormat := uint16(data[20]) | uint16(data[21])<<8
	numChannels := uint16(data[22]) | uint16(data[23])<<8
	sampleRate := uint32(data[24]) | uint32(data[25])<<8 | uint32(data[26])<<16 | uint32(data[27])<<24
	byteRate := uint32(data[28]) | uint32(data[29])<<8 | uint32(data[30])<<16 | uint32(data[31])<<24
	blockAlign := uint16(data[32]) | uint16(data[33])<<8
	bitsPerSample := uint16(data[34]) | uint16(data[35])<<8
	// data chunk
	if string(data[36:40]) != "data" {
		logger.Error("voice: WAV missing data chunk", "chunk", string(data[36:40]))
		return
	}
	dataSize := uint32(data[40]) | uint32(data[41])<<8 | uint32(data[42])<<16 | uint32(data[43])<<24
	logger.Info("voice: WAV analysis",
		"format", audioFormat,
		"channels", numChannels,
		"sampleRate", sampleRate,
		"byteRate", byteRate,
		"blockAlign", blockAlign,
		"bitsPerSample", bitsPerSample,
		"dataSize", dataSize,
		"fileSize", len(data),
	)
}

// saveFailedAudio writes the raw audio payload and metadata to disk when
// recognition fails, so the exact bytes can be inspected later.
func saveFailedAudio(audioType int32, rawData []byte, provider string, errMsg string) {
	ts := time.Now().UTC().Format("20060102_150405.000")
	base := fmt.Sprintf("%s_type%d_len%d", ts, audioType, len(rawData))

	dir := voiceDataDir("failures")
	_ = os.MkdirAll(dir, 0755)

	// Write raw binary.
	binPath := filepath.Join(dir, base+".bin")
	if writeErr := os.WriteFile(binPath, rawData, 0644); writeErr != nil {
		// Best-effort; log to stderr if even this fails.
		fmt.Fprintf(os.Stderr, "voice: failed to write %s: %v\n", binPath, writeErr)
	}

	// Write metadata JSON.
	meta := map[string]any{
		"timestamp": time.Now().UTC().Format(time.RFC3339Nano),
		"audioType": audioType,
		"dataLen":   len(rawData),
		"provider":  provider,
		"error":     errMsg,
		"binFile":   binPath,
	}
	metaPath := filepath.Join(dir, base+".meta.json")
	if metaBytes, marshalErr := json.MarshalIndent(meta, "", "  "); marshalErr == nil {
		_ = os.WriteFile(metaPath, metaBytes, 0644)
	}
}

// Actor manages voice (STT) configuration and recognition.
type Actor struct {
	actor.Host
	store   persist.Persist
	actorID string
}

// Type returns the actor type identifier.
func (a *Actor) Type() string { return "voice" }

// OnInit loads persisted configuration.
func (a *Actor) OnInit(ctx actor.Context) error {
	if a.store == nil {
		var err error
		a.store, err = persist.New(config.PersistConfig("voice"))
		if err != nil {
			return err
		}
	}
	a.actorID = ctx.Self().ID().String()
	if err := a.migrateLegacyConfig(); err != nil {
		ctx.Logger().Error("voice: legacy config migration failed", "err", err)
	}
	return nil
}

// OnStart registers callables.
func (a *Actor) OnStart(ctx actor.Context) error {
	ctx.Logger().Info("voice: starting", "id", a.actorID)

	if err := ctx.Register("voice.list_accounts", a.handleAccountList, actor.Public()); err != nil {
		return fmt.Errorf("voice: register list_accounts: %w", err)
	}
	if err := ctx.Register("voice.create_account", a.handleAccountCreate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("voice: register create_account: %w", err)
	}
	if err := ctx.Register("voice.update_account", a.handleAccountUpdate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("voice: register update_account: %w", err)
	}
	if err := ctx.Register("voice.delete_account", a.handleAccountDelete, actor.AdminOnly()); err != nil {
		return fmt.Errorf("voice: register delete_account: %w", err)
	}
	if err := ctx.Register("voice.activate_account", a.handleAccountActivate, actor.AdminOnly()); err != nil {
		return fmt.Errorf("voice: register activate_account: %w", err)
	}
	if err := ctx.Register("voice.recognize", a.handleRecognize, actor.Public()); err != nil {
		return fmt.Errorf("voice: register recognize: %w", err)
	}
	if err := ctx.Register("voice.synthesize", a.handleSynthesize, actor.Public()); err != nil {
		return fmt.Errorf("voice: register synthesize: %w", err)
	}
	if err := ctx.Register("voice.clone", a.handleVoiceClone, actor.AdminOnly()); err != nil {
		return fmt.Errorf("voice: register clone: %w", err)
	}
	if err := ctx.Register("voice.design", a.handleVoiceDesign, actor.AdminOnly()); err != nil {
		return fmt.Errorf("voice: register design: %w", err)
	}
	if err := ctx.Register("voice.config_export", a.handleConfigExport, actor.AdminOnly()); err != nil {
		return fmt.Errorf("voice: register config.export: %w", err)
	}
	if err := ctx.Register("voice.config_import", a.handleConfigImport, actor.AdminOnly()); err != nil {
		return fmt.Errorf("voice: register config.import: %w", err)
	}
	if err := ctx.Register("voice.get_notify_config", a.handleNotifyConfigGet, actor.Public()); err != nil {
		return fmt.Errorf("voice: register get_notify_config: %w", err)
	}
	if err := ctx.Register("voice.set_notify_config", a.handleNotifyConfigSet, actor.Public()); err != nil {
		return fmt.Errorf("voice: register set_notify_config: %w", err)
	}
	if err := ctx.Register("voice.set_hotwords", a.handleHotwordsSet, actor.Public()); err != nil {
		return fmt.Errorf("voice: register set_hotwords: %w", err)
	}
	if err := ctx.Register("voice.get_hotwords", a.handleHotwordsGet, actor.Public()); err != nil {
		return fmt.Errorf("voice: register get_hotwords: %w", err)
	}
	if err := ctx.Register("voice.delete_hotwords", a.handleHotwordsDelete, actor.Public()); err != nil {
		return fmt.Errorf("voice: register delete_hotwords: %w", err)
	}

	if err := ctx.RegisterDomain("voice").Expose(); err != nil {
		return fmt.Errorf("voice: expose service: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Account persistence
// ---------------------------------------------------------------------------

// voiceStore is the on-disk shape: a list of STT/TTS accounts plus the active
// id for each service kind. Serialized via json.Marshal (PascalCase tags).
type voiceStore struct {
	Accounts    []gen.VoiceAccount `json:"Accounts"`
	ActiveSTTID string             `json:"ActiveSTTId,omitempty"`
	ActiveTTSID string             `json:"ActiveTTSId,omitempty"`
}

// normalizeKind coerces empty/unknown kinds to "stt".
func normalizeKind(kind string) string {
	if kind == "tts" {
		return "tts"
	}
	return "stt"
}

// activeIDFor returns the stored active account id for a kind.
func (st *voiceStore) activeIDFor(kind string) string {
	if kind == "tts" {
		return st.ActiveTTSID
	}
	return st.ActiveSTTID
}

// setActiveIDFor sets the stored active account id for a kind.
func (st *voiceStore) setActiveIDFor(kind, id string) {
	if kind == "tts" {
		st.ActiveTTSID = id
		return
	}
	st.ActiveSTTID = id
}

func (a *Actor) loadStore() (voiceStore, error) {
	var st voiceStore
	if err := persist.LoadOrZero(a.store, a.actorID, &st); err != nil {
		return voiceStore{}, err
	}
	return st, nil
}

func (a *Actor) saveStore(st voiceStore) error {
	return a.store.Save(a.actorID, st)
}

// activeAccount returns the account used by the given service kind.
func (a *Actor) activeAccount(kind string) (gen.VoiceAccount, error) {
	kind = normalizeKind(kind)
	st, err := a.loadStore()
	if err != nil {
		return gen.VoiceAccount{}, err
	}
	activeID := st.activeIDFor(kind)
	for i := range st.Accounts {
		if st.Accounts[i].ID == activeID {
			return st.Accounts[i], nil
		}
	}
	return gen.VoiceAccount{}, fmt.Errorf("voice: no active %s account configured", kind)
}

// accountFor resolves the account for a call of the given service kind:
// accountID empty = the kind's active account (legacy behavior); accountID
// non-empty = that exact account, and it must be of the same kind — an stt
// call can never borrow a tts account and vice versa.
func (a *Actor) accountFor(kind, accountID string) (gen.VoiceAccount, error) {
	if accountID == "" {
		return a.activeAccount(kind)
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.VoiceAccount{}, err
	}
	for i := range st.Accounts {
		if st.Accounts[i].ID != accountID {
			continue
		}
		if normalizeKind(st.Accounts[i].Kind) != normalizeKind(kind) {
			return gen.VoiceAccount{}, fmt.Errorf("voice: account %s is kind %s, cannot serve %s", accountID, st.Accounts[i].Kind, kind)
		}
		return st.Accounts[i], nil
	}
	return gen.VoiceAccount{}, fmt.Errorf("voice: account %s not found", accountID)
}

func accountView(acc gen.VoiceAccount) gen.VoiceAccountView {
	return gen.VoiceAccountView{
		ID: acc.ID, Kind: acc.Kind, Name: acc.Name, Provider: acc.Provider, HasAPIKey: acc.APIKey != "", Model: acc.Model, Voice: acc.Voice, Language: acc.Language, BaseURL: acc.BaseURL, Proxy: acc.Proxy,
	}
}

// newAccountID returns a short random id, e.g. "va_1a2b3c4d".
func newAccountID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return "va_" + hex.EncodeToString(b[:])
}

// migrateLegacyConfig converts the pre-multi-account single-config shape
// (lowercase provider/apiKey/model/language keys) into one STT account and
// marks it active. Idempotent: no-op when accounts already exist or no legacy data.
func (a *Actor) migrateLegacyConfig() error {
	st, err := a.loadStore()
	if err != nil {
		return fmt.Errorf("load: %w", err)
	}
	if len(st.Accounts) > 0 {
		return nil
	}
	var m map[string]any
	if err := persist.LoadOrZero(a.store, a.actorID, &m); err != nil {
		return fmt.Errorf("load legacy: %w", err)
	}
	if m == nil {
		return nil
	}
	provider, _ := m["provider"].(string)
	if provider == "" {
		return nil
	}
	acc := gen.VoiceAccount{
		ID:       newAccountID(),
		Kind:     "stt",
		Name:     provider,
		Provider: provider,
		APIKey:   strVal(m["apiKey"]),
		Model:    strVal(m["model"]),
		Language: strVal(m["language"]),
	}
	st.Accounts = []gen.VoiceAccount{acc}
	st.ActiveSTTID = acc.ID
	return a.saveStore(st)
}

func strVal(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// ---------------------------------------------------------------------------
// Callable handlers
// ---------------------------------------------------------------------------

func (a *Actor) handleAccountList(_ actor.PureContext, req gen.VoiceAccountListReq) (gen.VoiceAccountListResp, error) {
	kind := normalizeKind(req.Kind)
	st, err := a.loadStore()
	if err != nil {
		return gen.VoiceAccountListResp{}, fmt.Errorf("voice: load accounts: %w", err)
	}
	views := make([]gen.VoiceAccountView, 0, len(st.Accounts))
	for i := range st.Accounts {
		if st.Accounts[i].Kind != kind {
			continue
		}
		views = append(views, accountView(st.Accounts[i]))
	}
	return gen.VoiceAccountListResp{Items: views, ActiveID: st.activeIDFor(kind)}, nil
}

func (a *Actor) handleAccountCreate(_ actor.PureContext, req gen.VoiceAccountCreateReq) (gen.VoiceAccountCreateResp, error) {
	if req.Name == "" || req.Provider == "" {
		return gen.VoiceAccountCreateResp{}, fmt.Errorf("voice: name and provider are required")
	}
	kind := normalizeKind(req.Kind)
	st, err := a.loadStore()
	if err != nil {
		return gen.VoiceAccountCreateResp{}, fmt.Errorf("voice: load accounts: %w", err)
	}
	acc := gen.VoiceAccount{
		ID:       newAccountID(),
		Kind:     kind,
		Name:     req.Name,
		Provider: req.Provider,
		APIKey:   req.APIKey,
		Model:    req.Model,
		Voice:    req.Voice,
		Language: req.Language,
		BaseURL:  req.BaseURL,
		Proxy:    req.Proxy,
	}
	st.Accounts = append(st.Accounts, acc)
	// First account of this kind becomes active automatically.
	if st.activeIDFor(kind) == "" {
		st.setActiveIDFor(kind, acc.ID)
	}
	if err := a.saveStore(st); err != nil {
		return gen.VoiceAccountCreateResp{}, fmt.Errorf("voice: save accounts: %w", err)
	}
	return gen.VoiceAccountCreateResp{Account: accountView(acc)}, nil
}

func (a *Actor) handleAccountUpdate(_ actor.PureContext, req gen.VoiceAccountUpdateReq) (gen.VoiceAccountUpdateResp, error) {
	if req.ID == "" {
		return gen.VoiceAccountUpdateResp{}, fmt.Errorf("voice: account id is required")
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.VoiceAccountUpdateResp{}, fmt.Errorf("voice: load accounts: %w", err)
	}
	idx := -1
	for i := range st.Accounts {
		if st.Accounts[i].ID == req.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return gen.VoiceAccountUpdateResp{}, fmt.Errorf("voice: account %q not found", req.ID)
	}
	acc := st.Accounts[idx]
	if req.Name != "" {
		acc.Name = req.Name
	}
	if req.Provider != "" {
		acc.Provider = req.Provider
	}
	// Empty ApiKey preserves the stored secret (redacted edit flow).
	if req.APIKey != "" {
		acc.APIKey = req.APIKey
	}
	if req.Model != "" {
		acc.Model = req.Model
	}
	if req.Voice != "" {
		acc.Voice = req.Voice
	}
	if req.Language != "" {
		acc.Language = req.Language
	}
	if req.BaseURL != "" {
		acc.BaseURL = req.BaseURL
	}
	if req.Proxy != "" {
		acc.Proxy = req.Proxy
	}
	st.Accounts[idx] = acc
	if err := a.saveStore(st); err != nil {
		return gen.VoiceAccountUpdateResp{}, fmt.Errorf("voice: save accounts: %w", err)
	}
	return gen.VoiceAccountUpdateResp{Account: accountView(acc)}, nil
}

func (a *Actor) handleAccountDelete(_ actor.PureContext, req gen.VoiceAccountDeleteReq) (gen.VoiceAccountDeleteResp, error) {
	if req.ID == "" {
		return gen.VoiceAccountDeleteResp{}, fmt.Errorf("voice: account id is required")
	}
	st, err := a.loadStore()
	if err != nil {
		return gen.VoiceAccountDeleteResp{}, fmt.Errorf("voice: load accounts: %w", err)
	}
	next := st.Accounts[:0]
	var deletedKind string
	for i := range st.Accounts {
		if st.Accounts[i].ID == req.ID {
			deletedKind = st.Accounts[i].Kind
			continue
		}
		next = append(next, st.Accounts[i])
	}
	if deletedKind == "" {
		return gen.VoiceAccountDeleteResp{}, fmt.Errorf("voice: account %q not found", req.ID)
	}
	st.Accounts = next
	// If the deleted account was the active one for its kind, fall back to the
	// first remaining account of that kind (if any).
	if deletedKind != "" && st.activeIDFor(deletedKind) == req.ID {
		fallbackID := ""
		for i := range st.Accounts {
			if st.Accounts[i].Kind == deletedKind {
				fallbackID = st.Accounts[i].ID
				break
			}
		}
		st.setActiveIDFor(deletedKind, fallbackID)
	}
	if err := a.saveStore(st); err != nil {
		return gen.VoiceAccountDeleteResp{}, fmt.Errorf("voice: save accounts: %w", err)
	}
	return gen.VoiceAccountDeleteResp{}, nil
}

func (a *Actor) handleAccountActivate(_ actor.PureContext, req gen.VoiceAccountActivateReq) (gen.VoiceAccountActivateResp, error) {
	if req.ID == "" {
		return gen.VoiceAccountActivateResp{}, fmt.Errorf("voice: account id is required")
	}
	kind := normalizeKind(req.Kind)
	st, err := a.loadStore()
	if err != nil {
		return gen.VoiceAccountActivateResp{}, fmt.Errorf("voice: load accounts: %w", err)
	}
	found := false
	for i := range st.Accounts {
		if st.Accounts[i].ID == req.ID {
			if st.Accounts[i].Kind != kind {
				return gen.VoiceAccountActivateResp{}, fmt.Errorf("voice: account %q is not a %s account", req.ID, kind)
			}
			found = true
			break
		}
	}
	if !found {
		return gen.VoiceAccountActivateResp{}, fmt.Errorf("voice: account %q not found", req.ID)
	}
	st.setActiveIDFor(kind, req.ID)
	if err := a.saveStore(st); err != nil {
		return gen.VoiceAccountActivateResp{}, fmt.Errorf("voice: save accounts: %w", err)
	}
	return gen.VoiceAccountActivateResp{ActiveID: req.ID}, nil
}

func (a *Actor) handleRecognize(ctx actor.PureContext, req gen.VoiceRecognizeReq) (gen.VoiceRecognizeResp, error) {
	logger := ctx.Logger()
	logger.Info("voice: recognize request", "audioType", req.Audio.AudioType, "dataLen", len(req.Audio.Data))

	// Save every incoming payload for analysis.
	binPath := saveIncomingAudio(req.Audio.AudioType, req.Audio.Data)
	logger.Info("voice: saved incoming audio", "path", binPath)

	// If WAV, analyze header.
	if req.Audio.AudioType == AudioTypeWav || req.Audio.AudioType == AudioTypePCM {
		analyzeWAV(req.Audio.Data, logger)
	}

	acc, err := a.accountFor("stt", req.AccountID)
	if err != nil {
		logger.Error("voice: load active account failed", "err", err)
		return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: %w", err)
	}
	logger.Info("voice: account loaded", "provider", acc.Provider, "model", acc.Model, "language", acc.Language)

	audioData, format, err := prepareAudio(req.Audio.AudioType, req.Audio.Data)
	if err != nil {
		errMsg := err.Error()
		logger.Error("voice: prepare audio failed", "audioType", req.Audio.AudioType, "dataLen", len(req.Audio.Data), "err", errMsg)
		saveFailedAudio(req.Audio.AudioType, req.Audio.Data, acc.Provider, errMsg)
		return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: %w", err)
	}
	logger.Info("voice: audio prepared", "audioType", req.Audio.AudioType, "format", format, "dataLen", len(req.Audio.Data), "preparedLen", len(audioData))

	switch acc.Provider {
	case "glm":
		text, err := recognizeGLM(audioData, format, acc, req.Hotwords)
		if err != nil {
			errMsg := err.Error()
			logger.Error("voice: glm recognize failed", "format", format, "dataLen", len(audioData), "err", errMsg)
			saveFailedAudio(req.Audio.AudioType, req.Audio.Data, acc.Provider, errMsg)
			return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: glm recognize: %w", err)
		}
		logger.Info("voice: glm recognize success", "textLen", len(text))
		return gen.VoiceRecognizeResp{Text: text}, nil
	case "openai", "openai_custom":
		text, err := recognizeOpenAI(audioData, format, acc, req.Hotwords)
		if err != nil {
			errMsg := err.Error()
			logger.Error("voice: openai recognize failed", "provider", acc.Provider, "format", format, "dataLen", len(audioData), "err", errMsg)
			saveFailedAudio(req.Audio.AudioType, req.Audio.Data, acc.Provider, errMsg)
			return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: openai recognize: %w", err)
		}
		logger.Info("voice: openai recognize success", "provider", acc.Provider, "textLen", len(text))
		return gen.VoiceRecognizeResp{Text: text}, nil
	case "baidu":
		text, err := recognizeBaidu(audioData, format, acc, req.Hotwords)
		if err != nil {
			errMsg := err.Error()
			logger.Error("voice: baidu recognize failed", "format", format, "dataLen", len(audioData), "err", errMsg)
			saveFailedAudio(req.Audio.AudioType, req.Audio.Data, acc.Provider, errMsg)
			return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: baidu recognize: %w", err)
		}
		logger.Info("voice: baidu recognize success", "textLen", len(text))
		return gen.VoiceRecognizeResp{Text: text}, nil
	case "minimax":
		text, err := recognizeMinimax(audioData, format, acc)
		if err != nil {
			errMsg := err.Error()
			logger.Error("voice: minimax recognize failed", "format", format, "dataLen", len(audioData), "err", errMsg)
			saveFailedAudio(req.Audio.AudioType, req.Audio.Data, acc.Provider, errMsg)
			return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: minimax recognize: %w", err)
		}
		logger.Info("voice: minimax recognize success", "textLen", len(text))
		return gen.VoiceRecognizeResp{Text: text}, nil
	case "xiaomi_mimo":
		text, err := recognizeMiMo(audioData, format, acc)
		if err != nil {
			errMsg := err.Error()
			logger.Error("voice: mimo recognize failed", "format", format, "dataLen", len(audioData), "err", errMsg)
			saveFailedAudio(req.Audio.AudioType, req.Audio.Data, acc.Provider, errMsg)
			return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: mimo recognize: %w", err)
		}
		logger.Info("voice: mimo recognize success", "textLen", len(text))
		return gen.VoiceRecognizeResp{Text: text}, nil
	case "qwen":
		text, err := recognizeQwen(audioData, format, acc, req.Hotwords)
		if err != nil {
			errMsg := err.Error()
			logger.Error("voice: qwen recognize failed", "format", format, "dataLen", len(audioData), "err", errMsg)
			saveFailedAudio(req.Audio.AudioType, req.Audio.Data, acc.Provider, errMsg)
			return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: qwen recognize: %w", err)
		}
		logger.Info("voice: qwen recognize success", "textLen", len(text))
		return gen.VoiceRecognizeResp{Text: text}, nil
	case "doubao":
		text, err := recognizeDoubao(audioData, format, acc)
		if err != nil {
			errMsg := err.Error()
			logger.Error("voice: doubao recognize failed", "format", format, "dataLen", len(audioData), "err", errMsg)
			saveFailedAudio(req.Audio.AudioType, req.Audio.Data, acc.Provider, errMsg)
			return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: doubao recognize: %w", err)
		}
		logger.Info("voice: doubao recognize success", "textLen", len(text))
		return gen.VoiceRecognizeResp{Text: text}, nil
	case "", "web_speech":
		logger.Error("voice: client-side provider requested on server", "provider", acc.Provider)
		return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: provider '%s' requires client-side processing", acc.Provider)
	default:
		logger.Error("voice: unknown provider", "provider", acc.Provider)
		return gen.VoiceRecognizeResp{}, fmt.Errorf("voice: unknown provider '%s'", acc.Provider)
	}
}

// ---------------------------------------------------------------------------
// Text-to-speech
// ---------------------------------------------------------------------------

// ttsAudioType maps a container format string to the shared AudioType constant.
func ttsAudioType(format string) int32 {
	switch format {
	case "wav":
		return AudioTypeWav
	default:
		return AudioTypeMP3
	}
}

func (a *Actor) handleSynthesize(ctx actor.PureContext, req gen.VoiceSynthesizeReq) (gen.VoiceSynthesizeResp, error) {
	logger := ctx.Logger()
	logger.Info("voice: synthesize request", "inputLen", len(req.Input), "model", req.Model, "voice", req.Voice, "format", req.Format)

	if req.Input == "" {
		return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: synthesize input is empty")
	}

	acc, err := a.accountFor("tts", req.AccountID)
	if err != nil {
		logger.Error("voice: synthesize load active account failed", "err", err)
		return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: %w", err)
	}
	// Fall back to account-level model/voice when the request omits them.
	if req.Model == "" {
		req.Model = acc.Model
	}
	if req.Voice == "" {
		req.Voice = acc.Voice
	}

	switch acc.Provider {
	case "glm":
		audio, format, err := synthesizeGLM(req, acc)
		if err != nil {
			logger.Error("voice: glm synthesize failed", "err", err)
			return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: glm synthesize: %w", err)
		}
		logger.Info("voice: glm synthesize success", "audioLen", len(audio), "format", format)
		return gen.VoiceSynthesizeResp{Audio: gen.VoiceAudio{AudioType: ttsAudioType(format), Data: audio}}, nil
	case "openai", "openai_custom":
		audio, format, err := synthesizeOpenAI(req, acc)
		if err != nil {
			logger.Error("voice: openai synthesize failed", "provider", acc.Provider, "err", err)
			return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: openai synthesize: %w", err)
		}
		logger.Info("voice: openai synthesize success", "provider", acc.Provider, "audioLen", len(audio), "format", format)
		return gen.VoiceSynthesizeResp{Audio: gen.VoiceAudio{AudioType: ttsAudioType(format), Data: audio}}, nil
	case "openai_chat_audio":
		audio, format, err := synthesizeOpenAIChat(req, acc)
		if err != nil {
			logger.Error("voice: openai chat-audio synthesize failed", "provider", acc.Provider, "err", err)
			return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: openai chat-audio synthesize: %w", err)
		}
		logger.Info("voice: openai chat-audio synthesize success", "provider", acc.Provider, "audioLen", len(audio), "format", format)
		return gen.VoiceSynthesizeResp{Audio: gen.VoiceAudio{AudioType: ttsAudioType(format), Data: audio}}, nil
	case "minimax":
		audio, format, err := synthesizeMinimax(req, acc)
		if err != nil {
			logger.Error("voice: minimax synthesize failed", "err", err)
			return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: minimax synthesize: %w", err)
		}
		logger.Info("voice: minimax synthesize success", "audioLen", len(audio), "format", format)
		return gen.VoiceSynthesizeResp{Audio: gen.VoiceAudio{AudioType: ttsAudioType(format), Data: audio}}, nil
	case "xiaomi_mimo":
		audio, format, err := synthesizeMiMo(req, acc)
		if err != nil {
			logger.Error("voice: mimo synthesize failed", "err", err)
			return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: mimo synthesize: %w", err)
		}
		logger.Info("voice: mimo synthesize success", "audioLen", len(audio), "format", format)
		return gen.VoiceSynthesizeResp{Audio: gen.VoiceAudio{AudioType: ttsAudioType(format), Data: audio}}, nil
	case "qwen":
		audio, format, err := synthesizeQwen(req, acc)
		if err != nil {
			logger.Error("voice: qwen synthesize failed", "err", err)
			return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: qwen synthesize: %w", err)
		}
		logger.Info("voice: qwen synthesize success", "audioLen", len(audio), "format", format)
		return gen.VoiceSynthesizeResp{Audio: gen.VoiceAudio{AudioType: ttsAudioType(format), Data: audio}}, nil
	case "doubao":
		audio, format, err := synthesizeDoubao(req, acc)
		if err != nil {
			logger.Error("voice: doubao synthesize failed", "provider", acc.Provider, "err", err)
			return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: doubao synthesize: %w", err)
		}
		logger.Info("voice: doubao synthesize success", "provider", acc.Provider, "audioLen", len(audio), "format", format)
		return gen.VoiceSynthesizeResp{Audio: gen.VoiceAudio{AudioType: ttsAudioType(format), Data: audio}}, nil
	case "", "web_speech":
		return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: provider '%s' cannot synthesize server-side", acc.Provider)
	default:
		return gen.VoiceSynthesizeResp{}, fmt.Errorf("voice: tts not implemented for provider '%s'", acc.Provider)
	}
}

// ---------------------------------------------------------------------------
// HTTP helpers for STT providers
// ---------------------------------------------------------------------------

// sttHTTPClient returns the HTTP client for voice provider calls. When proxy is
// a non-empty HTTP(S)/SOCKS5 URL, the returned client dials through it while
// preserving the 60s timeout; the underlying transport is the shared per-proxy
// connection pool from llmclient.
func sttHTTPClient(proxy string) (*http.Client, error) {
	if proxy == "" {
		return &http.Client{Timeout: 60 * time.Second}, nil
	}
	c, err := llmclient.HTTPClientForProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("voice: %w", err)
	}
	return &http.Client{Timeout: 60 * time.Second, Transport: c.Transport}, nil
}

func buildMultipartBody(audioData []byte, format string, fields map[string]string) ([]byte, string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)

	for k, v := range fields {
		if err := mw.WriteField(k, v); err != nil {
			return nil, "", err
		}
	}

	// Determine filename extension from format.
	ext := ".wav"
	switch format {
	case "aac", "m4a":
		ext = ".m4a"
	case "mp3":
		ext = ".mp3"
	case "ogg":
		ext = ".ogg"
	case "webm":
		ext = ".webm"
	case "pcm":
		ext = ".pcm"
	}

	fw, err := mw.CreateFormFile("file", "recording"+ext)
	if err != nil {
		return nil, "", err
	}
	if _, err := fw.Write(audioData); err != nil {
		return nil, "", err
	}
	mw.Close()

	return buf.Bytes(), mw.FormDataContentType(), nil
}

func extractTextFromJSON(body []byte) (string, error) {
	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", err
	}
	// Support {"text":"..."} and {"success":true,"data":{"text":"..."}}
	text, ok := result["text"].(string)
	if !ok || text == "" {
		if data, ok2 := result["data"].(map[string]interface{}); ok2 {
			text, ok = data["text"].(string)
		}
	}
	if !ok || strings.TrimSpace(text) == "" || strings.TrimSpace(text) == "#" {
		if errMsg, ok := result["error"].(string); ok {
			return "", fmt.Errorf("stt error: %s", errMsg)
		}
		return "", fmt.Errorf("stt returned no text")
	}
	return text, nil
}

func doSTTRequest(req *http.Request, proxy string) (string, error) {
	client, err := sttHTTPClient(proxy)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("http %d: %s", resp.StatusCode, truncate(string(body)))
	}

	return extractTextFromJSON(body)
}

// handleConfigExport serializes the account list (redacted) to JSON.
func (a *Actor) handleConfigExport(_ actor.PureContext) (gen.VoiceConfigExportResp, error) {
	st, err := a.loadStore()
	if err != nil {
		return gen.VoiceConfigExportResp{}, fmt.Errorf("voice: load accounts: %w", err)
	}
	views := make([]gen.VoiceAccountView, 0, len(st.Accounts))
	for i := range st.Accounts {
		views = append(views, accountView(st.Accounts[i]))
	}
	b, err := json.MarshalIndent(struct {
		Items       []gen.VoiceAccountView `json:"Accounts"`
		ActiveSTTID string                 `json:"ActiveSTTId,omitempty"`
		ActiveTTSID string                 `json:"ActiveTTSId,omitempty"`
	}{Items: views, ActiveSTTID: st.ActiveSTTID, ActiveTTSID: st.ActiveTTSID}, "", "  ")
	if err != nil {
		return gen.VoiceConfigExportResp{}, fmt.Errorf("voice: marshal accounts: %w", err)
	}
	return gen.VoiceConfigExportResp{Data: string(b)}, nil
}

// handleConfigImport replaces the account list from JSON.
func (a *Actor) handleConfigImport(_ actor.PureContext, req gen.VoiceConfigImportReq) (gen.VoiceConfigImportResp, error) {
	var st voiceStore
	if err := json.Unmarshal([]byte(req.Data), &st); err != nil {
		return gen.VoiceConfigImportResp{}, fmt.Errorf("voice: unmarshal accounts: %w", err)
	}
	if err := a.saveStore(st); err != nil {
		return gen.VoiceConfigImportResp{}, fmt.Errorf("voice: save accounts: %w", err)
	}
	return gen.VoiceConfigImportResp{}, nil
}

// ---------------------------------------------------------------------------
// Notification sound config persistence
// ---------------------------------------------------------------------------

// defaultNotifyConfig returns sensible defaults for a first-time user.
func defaultNotifyConfig() gen.VoiceNotifyConfig {
	return gen.VoiceNotifyConfig{
		Enabled:       true,
		Volume:        70,
		Sound:         "chime",
		OnComplete:    true,
		OnError:       true,
		OnInteraction: true,
		OnAllComplete: true,
	}
}

func (a *Actor) notifyConfigKey() string {
	return a.actorID + "/notify-config"
}

// migrateVoiceCardIfNeeded is a ONE-TIME migration from a legacy persist key
// to a new key. If the new key already exists the call is a no-op; if the old
// key exists its raw JSON is copied to the new key and the old key is
// deleted, so subsequent loads skip this path entirely.
//
// ONE-TIME MIGRATION — remove after verification.
func migrateVoiceCardIfNeeded(store persist.Persist, oldName, newName string) {
	var probe json.RawMessage
	if err := store.Load(newName, &probe); err == nil {
		return
	} else if !errors.Is(err, persist.ErrNotExist) {
		return
	}
	var data json.RawMessage
	if err := store.Load(oldName, &data); err != nil {
		return
	}
	_ = store.Save(newName, data)
	_ = store.Delete(oldName)
}

func (a *Actor) loadNotifyConfig() (gen.VoiceNotifyConfig, error) {
	// ONE-TIME MIGRATION — remove after verification.
	// Migrate from the old key (actorID/notify) to the new key
	// (actorID/notify-config) so cascade Delete(actorID) reclaims it.
	migrateVoiceCardIfNeeded(a.store, a.actorID+"/notify", a.notifyConfigKey())

	var m map[string]any
	if err := persist.LoadOrZero(a.store, a.notifyConfigKey(), &m); err != nil {
		return gen.VoiceNotifyConfig{}, err
	}
	if m == nil {
		return defaultNotifyConfig(), nil
	}
	cfg := defaultNotifyConfig()
	if v, ok := m["enabled"].(bool); ok {
		cfg.Enabled = v
	}
	if v, ok := m["volume"].(float64); ok {
		cfg.Volume = int32(v)
	}
	if v, ok := m["sound"].(string); ok && v != "" {
		cfg.Sound = v
	}
	if v, ok := m["onComplete"].(bool); ok {
		cfg.OnComplete = v
	}
	if v, ok := m["onError"].(bool); ok {
		cfg.OnError = v
	}
	if v, ok := m["onInteraction"].(bool); ok {
		cfg.OnInteraction = v
	}
	if v, ok := m["onAllComplete"].(bool); ok {
		cfg.OnAllComplete = v
	}
	if v, ok := m["customSoundComplete"].(string); ok {
		cfg.CustomSoundComplete = v
	}
	if v, ok := m["customSoundError"].(string); ok {
		cfg.CustomSoundError = v
	}
	if v, ok := m["customSoundInteraction"].(string); ok {
		cfg.CustomSoundInteraction = v
	}
	if v, ok := m["customSoundAllComplete"].(string); ok {
		cfg.CustomSoundAllComplete = v
	}
	if v, ok := m["soundError"].(string); ok {
		cfg.SoundError = v
	}
	if v, ok := m["soundInteraction"].(string); ok {
		cfg.SoundInteraction = v
	}
	if v, ok := m["soundAllComplete"].(string); ok {
		cfg.SoundAllComplete = v
	}
	if v, ok := m["volumeComplete"].(float64); ok {
		cfg.VolumeComplete = int32(v)
	}
	if v, ok := m["volumeError"].(float64); ok {
		cfg.VolumeError = int32(v)
	}
	if v, ok := m["volumeInteraction"].(float64); ok {
		cfg.VolumeInteraction = int32(v)
	}
	if v, ok := m["volumeAllComplete"].(float64); ok {
		cfg.VolumeAllComplete = int32(v)
	}
	return cfg, nil
}

func (a *Actor) saveNotifyConfig(cfg gen.VoiceNotifyConfig) error {
	if cfg.Volume < 0 {
		cfg.Volume = 0
	}
	if cfg.Volume > 100 {
		cfg.Volume = 100
	}
	if cfg.Sound == "" {
		cfg.Sound = "chime"
	}
	return a.store.Save(a.notifyConfigKey(), map[string]any{
		"enabled":                cfg.Enabled,
		"volume":                 cfg.Volume,
		"sound":                  cfg.Sound,
		"onComplete":             cfg.OnComplete,
		"onError":                cfg.OnError,
		"onInteraction":          cfg.OnInteraction,
		"onAllComplete":          cfg.OnAllComplete,
		"soundError":             cfg.SoundError,
		"soundInteraction":       cfg.SoundInteraction,
		"soundAllComplete":       cfg.SoundAllComplete,
		"volumeComplete":         cfg.VolumeComplete,
		"volumeError":            cfg.VolumeError,
		"volumeInteraction":      cfg.VolumeInteraction,
		"volumeAllComplete":      cfg.VolumeAllComplete,
		"customSoundComplete":    cfg.CustomSoundComplete,
		"customSoundError":       cfg.CustomSoundError,
		"customSoundInteraction": cfg.CustomSoundInteraction,
		"customSoundAllComplete": cfg.CustomSoundAllComplete,
	})
}

func (a *Actor) handleNotifyConfigGet(_ actor.PureContext) (gen.VoiceNotifyConfigResp, error) {
	cfg, err := a.loadNotifyConfig()
	if err != nil {
		return gen.VoiceNotifyConfigResp{}, fmt.Errorf("voice: load notify config: %w", err)
	}
	return gen.VoiceNotifyConfigResp{Config: cfg}, nil
}

func (a *Actor) handleNotifyConfigSet(_ actor.PureContext, cfg gen.VoiceNotifyConfig) (gen.VoiceNotifyConfigResp, error) {
	if err := a.saveNotifyConfig(cfg); err != nil {
		return gen.VoiceNotifyConfigResp{}, fmt.Errorf("voice: save notify config: %w", err)
	}
	saved, err := a.loadNotifyConfig()
	if err != nil {
		return gen.VoiceNotifyConfigResp{}, fmt.Errorf("voice: reload notify config: %w", err)
	}
	return gen.VoiceNotifyConfigResp{Config: saved}, nil
}

// ---------------------------------------------------------------------------
// STT hotwords binding (per project/agent scope)
// ---------------------------------------------------------------------------

// hotwordsKey returns the persist sub-key for a project/agent scope.
// When agentID is empty, the binding is project-wide; when both are empty
// it is global.
func (a *Actor) hotwordsKey(projectID, agentID string) string {
	if agentID == "" {
		agentID = "global"
	}
	// Slash-joined, not filepath.Join: OS separators would become literal
	// path bytes in the persist name on non-Windows and break cascade delete.
	return a.actorID + "/hotwords/" + projectID + "/" + agentID
}

func (a *Actor) loadHotwords(projectID, agentID string) (gen.VoiceHotwordsBinding, error) {
	var m map[string]any
	key := a.hotwordsKey(projectID, agentID)
	if err := persist.LoadOrZero(a.store, key, &m); err != nil {
		return gen.VoiceHotwordsBinding{}, err
	}
	binding := gen.VoiceHotwordsBinding{ProjectID: projectID, AgentID: agentID}
	if m == nil {
		return binding, nil
	}
	if v, ok := m["hotwords"].([]interface{}); ok {
		for _, item := range v {
			if s, ok := item.(string); ok {
				binding.Hotwords = append(binding.Hotwords, s)
			}
		}
	}
	return binding, nil
}

func (a *Actor) saveHotwords(projectID, agentID string, hotwords []string) error {
	key := a.hotwordsKey(projectID, agentID)
	return a.store.Save(key, map[string]any{
		"projectId": projectID,
		"agentId":   agentID,
		"hotwords":  hotwords,
	})
}

func (a *Actor) deleteHotwords(projectID, agentID string) error {
	return a.store.Delete(a.hotwordsKey(projectID, agentID))
}

func (a *Actor) handleHotwordsSet(_ actor.PureContext, req gen.VoiceHotwordsSetReq) (gen.VoiceHotwordsSetResp, error) {
	if err := a.saveHotwords(req.ProjectID, req.AgentID, req.Hotwords); err != nil {
		return gen.VoiceHotwordsSetResp{}, fmt.Errorf("voice: save hotwords: %w", err)
	}
	binding, err := a.loadHotwords(req.ProjectID, req.AgentID)
	if err != nil {
		return gen.VoiceHotwordsSetResp{}, fmt.Errorf("voice: reload hotwords: %w", err)
	}
	return gen.VoiceHotwordsSetResp{Binding: binding}, nil
}

func (a *Actor) handleHotwordsGet(_ actor.PureContext, req gen.VoiceHotwordsGetReq) (gen.VoiceHotwordsGetResp, error) {
	binding, err := a.loadHotwords(req.ProjectID, req.AgentID)
	if err != nil {
		return gen.VoiceHotwordsGetResp{}, fmt.Errorf("voice: load hotwords: %w", err)
	}
	return gen.VoiceHotwordsGetResp{Binding: binding}, nil
}

func (a *Actor) handleHotwordsDelete(_ actor.PureContext, req gen.VoiceHotwordsDeleteReq) (gen.VoiceHotwordsDeleteResp, error) {
	if err := a.deleteHotwords(req.ProjectID, req.AgentID); err != nil {
		return gen.VoiceHotwordsDeleteResp{}, fmt.Errorf("voice: delete hotwords: %w", err)
	}
	return gen.VoiceHotwordsDeleteResp{}, nil
}
