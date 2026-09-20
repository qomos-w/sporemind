package voice

import (
	"encoding/binary"
)

// AudioType constants — frontend and backend must agree on these values.
const (
	AudioTypeWebm = 1 // webm/opus container
	AudioTypePCM  = 2 // raw 16-bit signed LE PCM, 16kHz mono
	AudioTypeWav  = 3 // WAV file (16-bit PCM)
	AudioTypeMP3  = 4 // MP3 file
	AudioTypeOgg  = 5 // OGG/Opus container
	AudioTypeM4A  = 6 // AAC in M4A container (Capacitor native output)
)

// prepareAudio normalizes audio data for STT providers.
// PCM is wrapped in a WAV header; WAV and M4A are passed through as-is.
// Returns the prepared data and the format string to pass to the provider.
func prepareAudio(audioType int32, data []byte) ([]byte, string, error) {
	switch audioType {
	case AudioTypePCM:
		wav, err := pcmToWav(data, 16000)
		return wav, "wav", err
	case AudioTypeWav:
		return data, "wav", nil
	case AudioTypeM4A:
		return data, "m4a", nil
	default:
		return nil, "", errUnsupportedFormat
	}
}

// pcmToWav wraps 16-bit mono PCM in a WAV header.
func pcmToWav(pcm []byte, sampleRate int) ([]byte, error) {
	dataSize := len(pcm)
	buf := make([]byte, 44+dataSize)

	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")

	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1) // mono
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate)*2) // byte rate
	binary.LittleEndian.PutUint16(buf[32:34], 2)                    // block align
	binary.LittleEndian.PutUint16(buf[34:36], 16)                   // bits per sample

	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))

	copy(buf[44:], pcm)
	return buf, nil
}
