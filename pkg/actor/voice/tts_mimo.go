package voice

import (
	"fmt"

	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
)

const (
	mimoTTSModel  = "mimo-v2.5-tts"
	mimoTTSVoice  = "Chloe"
	mimoTTSFormat = "mp3"
)

// synthesizeMiMo synthesizes speech via the MiMo chat-completions audio-modality
// endpoint. It is a thin wrapper around synthesizeOpenAIChat that supplies MiMo
// defaults for model, voice and format when the account/request omits them.
func synthesizeMiMo(req gen.VoiceSynthesizeReq, acc gen.VoiceAccount) ([]byte, string, error) {
	if acc.APIKey == "" {
		return nil, "", fmt.Errorf("mimo tts: api_key not configured")
	}

	if req.Model == "" {
		req.Model = mimoTTSModel
	}
	if req.Voice == "" {
		req.Voice = mimoTTSVoice
	}
	if req.Format == "" {
		req.Format = mimoTTSFormat
	}

	audio, format, err := synthesizeOpenAIChat(req, acc)
	if err != nil {
		return nil, "", fmt.Errorf("mimo tts: %w", err)
	}
	return audio, format, nil
}
