package voice

import (
	"encoding/binary"
	gen "github.com/qomos-w/sporemind/pkg/domain/gen"
	"os"
	"testing"
)

func testGLMAPIKey() string {
	return os.Getenv("SPOREMIND_GLM_TEST_API_KEY")
}

// generateWav produces a valid WAV file with a 440 Hz sine wave (A4 note).
// duration = seconds, sampleRate = Hz (e.g. 16000), amp = 0..32767.
func generateWav(duration float64, sampleRate int, amp int16) []byte {
	numSamples := int(duration * float64(sampleRate))
	dataSize := numSamples * 2 // 16-bit = 2 bytes per sample

	// RIFF header
	buf := make([]byte, 44+dataSize)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize)) // file size - 8
	copy(buf[8:12], "WAVE")

	// fmt chunk
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16) // chunk size
	binary.LittleEndian.PutUint16(buf[20:22], 1)  // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1)  // mono
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate)*2) // byte rate
	binary.LittleEndian.PutUint16(buf[32:34], 2)                    // block align
	binary.LittleEndian.PutUint16(buf[34:36], 16)                   // bits per sample

	// data chunk
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))

	for i := 0; i < numSamples; i++ {
		t := float64(i) / float64(sampleRate)
		val := int16(float64(amp) * sin(2*3.141592653589793*440*t))
		binary.LittleEndian.PutUint16(buf[44+i*2:], uint16(val))
	}
	return buf
}

// sin computes sine using Taylor series (no math dependency needed for test).
func sin(x float64) float64 {
	// Normalize to [-pi, pi]
	x = x - 6.283185307179586*float64(int(x/6.283185307179586))
	if x > 3.141592653589793 {
		x -= 6.283185307179586
	}
	if x < -3.141592653589793 {
		x += 6.283185307179586
	}
	// Taylor: sin(x) = x - x³/6 + x⁵/120 - x⁷/5040
	x2 := x * x
	return x * (1 - x2/6*(1-x2/20*(1-x2/42)))
}

func TestRecognizeGLM_SyntheticWav(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real API test in short mode")
	}
	if testGLMAPIKey() == "" {
		t.Skip("SPOREMIND_GLM_TEST_API_KEY not set, skipping GLM ASR test")
	}

	wav := generateWav(1.0, 16000, 16000)
	cfg := gen.VoiceAccount{
		ID:       "va_glm",
		Name:     "glm",
		Provider: "glm",
		APIKey:   testGLMAPIKey(),
		Model:    "glm-asr-2512",
		Language: "zh-CN",
	}

	text, err := recognizeGLM(wav, "wav", cfg, nil)
	if err != nil {
		t.Fatalf("recognizeGLM error: %v", err)
	}
	t.Logf("GLM ASR result: %q", text)
	if text == "" {
		t.Error("expected non-empty transcription, got empty string")
	}
}

// TestRecognizeGLM_PCMThroughWav tests the full pipeline: raw PCM → prepareAudio → GLM ASR.
func TestRecognizeGLM_PCMThroughWav(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real API test in short mode")
	}
	if testGLMAPIKey() == "" {
		t.Skip("SPOREMIND_GLM_TEST_API_KEY not set, skipping GLM ASR test")
	}

	// Generate raw PCM (same data as generateWav but without the 44-byte header).
	wav := generateWav(1.0, 16000, 16000)
	rawPCM := wav[44:]

	wavData, format, err := prepareAudio(AudioTypePCM, rawPCM)
	if err != nil {
		t.Fatalf("prepareAudio error: %v", err)
	}
	if format != "wav" {
		t.Fatalf("prepareAudio format: got %q, want wav", format)
	}

	cfg := gen.VoiceAccount{
		ID:       "va_glm",
		Name:     "glm",
		Provider: "glm",
		APIKey:   testGLMAPIKey(),
		Model:    "glm-asr-2512",
		Language: "zh-CN",
	}

	text, err := recognizeGLM(wavData, "wav", cfg, nil)
	if err != nil {
		t.Fatalf("recognizeGLM error: %v", err)
	}
	t.Logf("GLM ASR result (PCM→WAV): %q", text)
	if text == "" {
		t.Error("expected non-empty transcription, got empty string")
	}
}
