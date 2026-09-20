package tokenest

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"sync"

	tiktoken "github.com/pkoukk/tiktoken-go"
)

// Embedded BPE mergeable-ranks file shipped by OpenAI (SHA256 verified at
// download time). Embedding it keeps tokenization fully offline: no runtime
// fetch from openaipublic.blob.core.windows.net.
//
// Only cl100k_base is embedded. It is a good cross-family approximation for
// every model sporemind currently targets (Claude, Gemini, DeepSeek, GPT-4,
// GPT-3.5 and the OpenAI o/GPT-4o families, which are no longer primary).
// Embedding o200k_base as well would add ~3.6 MB of binary for negligible
// accuracy gain; real provider/probe usage always wins over these estimates.

//go:embed data/cl100k_base.tiktoken
var cl100kBaseBPEFile []byte

const cl100kBPEURL = "https://openaipublic.blob.core.windows.net/encodings/cl100k_base.tiktoken"

var (
	embeddedBPEOnce sync.Once
	embeddedCL100K  map[string]int
	embeddedBPEErr  error
)

func parseEmbeddedBPE() {
	embeddedCL100K, embeddedBPEErr = parseTiktokenBPEBytes(cl100kBaseBPEFile)
}

// embeddedBpeLoader serves the embedded cl100k_base ranks from memory. Any other
// URL falls back to tiktoken-go's default loader (network), so the tokenizer
// still degrades gracefully for encodings we did not embed.
type embeddedBpeLoader struct{}

func (embeddedBpeLoader) LoadTiktokenBpe(url string) (map[string]int, error) {
	if url == cl100kBPEURL {
		embeddedBPEOnce.Do(parseEmbeddedBPE)
		if embeddedBPEErr != nil {
			return nil, fmt.Errorf("tokenest: embedded cl100k_base BPE: %w", embeddedBPEErr)
		}
		return embeddedCL100K, nil
	}
	return tiktoken.NewDefaultBpeLoader().LoadTiktokenBpe(url)
}

// parseTiktokenBPEBytes parses the .tiktoken file format (base64 token + rank
// per line) directly from in-memory bytes. Mirrors tiktoken-go's
// loadTiktokenBpe but never touches the network or filesystem cache.
func parseTiktokenBPEBytes(data []byte) (map[string]int, error) {
	ranks := make(map[string]int, 200000)
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		idx := strings.IndexByte(line, ' ')
		if idx < 0 {
			return nil, fmt.Errorf("malformed BPE line: %q", line)
		}
		raw, err := base64.StdEncoding.DecodeString(line[:idx])
		if err != nil {
			return nil, fmt.Errorf("decode BPE token: %w", err)
		}
		rank, err := strconv.Atoi(line[idx+1:])
		if err != nil {
			return nil, fmt.Errorf("decode BPE rank: %w", err)
		}
		ranks[string(raw)] = rank
	}
	return ranks, nil
}

func init() {
	tiktoken.SetBpeLoader(embeddedBpeLoader{})
}
