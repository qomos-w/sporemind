package appbinding

// Voice capabilities. Speech-to-text and text-to-speech are independent
// services with independent account lists and active accounts — one account
// configuration must never serve both recognize and synthesize (project
// rule), so they gate as two separate capabilities. Both callIDs are
// pass-through: the wire IS the backing voice actor callable schema
// (gen.VoiceRecognizeReq/Resp, gen.VoiceSynthesizeReq/Resp in
// pkg/domain/gen), so no adapted wire contract is declared and codegen
// resolves types from the embedded manifest schema IDs.

func init() {
	RegisterCapability(CapabilityDef{
		ID:                 CapVoiceSTT,
		Title:              "语音识别（STT）",
		Description:        "允许应用调用语音识别服务，将音频转写为文本（消耗账户额度）。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.voice.stt.title",
		I18nDescriptionKey: "capability.voice.stt.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "语音识别（STT）", Description: "允许应用调用语音识别服务，将音频转写为文本（消耗账户额度）。"},
			"en-US": {Title: "Speech-to-text (STT)", Description: "Allows the app to call speech recognition services, transcribing audio into text (consumes account quota)."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "voice.recognize",
		Capability: CapVoiceSTT,
	})

	RegisterCapability(CapabilityDef{
		ID:                 CapVoiceTTS,
		Title:              "语音合成（TTS）",
		Description:        "允许应用调用语音合成服务，将文本转写为音频（消耗账户额度）。",
		RiskLevel:          RiskMedium,
		I18nTitleKey:       "capability.voice.tts.title",
		I18nDescriptionKey: "capability.voice.tts.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "语音合成（TTS）", Description: "允许应用调用语音合成服务，将文本转写为音频（消耗账户额度）。"},
			"en-US": {Title: "Text-to-speech (TTS)", Description: "Allows the app to call speech synthesis services, converting text into audio (consumes account quota)."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "voice.synthesize",
		Capability: CapVoiceTTS,
	})

	// voice.read — enumerate voice accounts (redacted). Pass-through wire to
	// the voice actor's own list callable (gen.VoiceAccountListReq/Resp; API
	// keys are never returned — accountView replaces them with HasApiKey).
	// Account management (create/update/delete/activate) stays AdminOnly on
	// the voice actor and is intentionally NOT exposed here.
	RegisterCapability(CapabilityDef{
		ID:                 CapVoiceRead,
		Title:              "枚举语音账户",
		Description:        "允许应用枚举宿主语音账户（stt/tts 分列表，返回名称/供应商/模型等元数据，不含 API 密钥）。",
		RiskLevel:          RiskLow,
		I18nTitleKey:       "capability.voice.read.title",
		I18nDescriptionKey: "capability.voice.read.description",
		Locales: map[string]CapabilityLocale{
			"zh-CN": {Title: "枚举语音账户", Description: "允许应用枚举宿主语音账户（stt/tts 分列表，返回名称/供应商/模型等元数据，不含 API 密钥）。"},
			"en-US": {Title: "List voice accounts", Description: "Allows the app to enumerate host voice accounts (separate stt/tts lists; metadata like name/provider/model, never API keys)."},
		},
	})
	RegisterHostCall(HostCallDef{
		CallID:     "voice.accounts.list",
		Capability: CapVoiceRead,
		Target:     "voice.list_accounts",
	})
}
