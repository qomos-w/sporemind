package logging

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/qomos-w/gospore/gateway"
)

func TestCaptureStdlibTeesToExtraWriter(t *testing.T) {
	origFlags := log.Flags()
	origOutput := log.Writer()
	defer func() {
		log.SetFlags(origFlags)
		log.SetOutput(origOutput)
	}()

	r := NewRing(100)
	var buf bytes.Buffer
	CaptureStdlib(r, &buf)

	log.Print("hello tee")

	deadline := time.Now().Add(2 * time.Second)
	for {
		if strings.Contains(buf.String(), "hello tee") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tee writer never received the line, got %q", buf.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	entries := r.QueryLogs(gateway.LogQuery{})
	found := false
	for _, e := range entries {
		if e.Message == "hello tee" {
			found = true
		}
	}
	if !found {
		t.Fatalf("ring buffer missing the line, got %v", entries)
	}
}
