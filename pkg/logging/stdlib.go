package logging

import (
	"io"
	"log"
	"time"

	"github.com/qomos-w/gospore/gateway"
)

// CaptureStdlib redirects stdlib log output into the ring buffer.
// This catches log.Printf/log.Println calls from non-actor code
// (e.g. gateway/ws.go debug logs or third-party libraries).
// Extra writers, when given, receive a copy of every captured line
// (e.g. os.Stderr in dev desktop builds so logs reach the terminal).
func CaptureStdlib(ring *Ring, tee ...io.Writer) {
	r, w := io.Pipe()
	log.SetFlags(0)
	log.SetOutput(w)

	go func() {
		var buf [4096]byte
		for {
			n, err := r.Read(buf[:])
			if err != nil {
				return
			}
			line := string(buf[:n])
			if len(line) > 0 && line[len(line)-1] == '\n' {
				line = line[:len(line)-1]
			}
			if line == "" {
				continue
			}
			for _, dst := range tee {
				if dst != nil {
					io.WriteString(dst, line+"\n")
				}
			}
			ring.Append(gateway.LogEntry{
				Timestamp: time.Now().Format(time.RFC3339Nano),
				Level:     "info",
				Message:   line,
			})
		}
	}()
}
