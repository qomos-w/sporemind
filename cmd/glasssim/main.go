// glasssim is a simulated MentraOS glasses device over the real wire: it
// connects to a running sporemind gateway (e.g. make dev-desktop) and drives
// the full glass surface — claim, event subscription, telemetry, speech
// uplink, interaction reports — without any hardware.
//
// Examples:
//
//	go run ./cmd/glasssim -addr 127.0.0.1:18080 -key "$SPOREMIND_GLASS_KEY"
//	go run ./cmd/glasssim -addr 127.0.0.1:18080 -key k -speech silence -interact opt-0:select:0 -once
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/qomos-w/sporemind/pkg/actor/glassinteract/glasssim"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:18080", "gateway host:port")
	key := flag.String("key", os.Getenv("SPOREMIND_GLASS_KEY"), "glass bootstrap key (default $SPOREMIND_GLASS_KEY)")
	device := flag.String("device", "glasssim-001", "device id")
	battery := flag.Int("battery", 77, "battery level reported by telemetry")
	interval := flag.Duration("interval", 30*time.Second, "telemetry report period")
	speech := flag.String("speech", "", "send one utterance: 'silence' or a raw PCM file path (16 kHz mono s16le)")
	speechMS := flag.Int("speech-ms", 500, "silence duration when -speech silence")
	interact := flag.String("interact", "", "report one interaction elementID:action[:value], e.g. opt-0:select:0")
	once := flag.Bool("once", false, "exit after the startup cycle instead of staying connected")
	flag.Parse()

	if *key == "" {
		fmt.Fprintln(os.Stderr, "glasssim: -key or $SPOREMIND_GLASS_KEY is required")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dev, err := glasssim.Connect(ctx, *addr, *key, *device)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer dev.Close()

	claim, err := dev.Claim()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("connected: session=%s generation=%d online=%v\n", claim.SessionID, claim.Generation, claim.Online)

	if err := dev.SubscribeEvents(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("subscribed: %v\n", glasssim.EventKinds)

	if *speech != "" {
		pcm := glasssim.Silence(*speechMS)
		if *speech != "silence" {
			data, err := os.ReadFile(*speech)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			pcm = data
		}
		if err := dev.Speech(pcm, 0); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("speech: %d bytes sent and acked\n", len(pcm))
	}

	if *interact != "" {
		var element, action, value string
		n, _ := fmt.Sscanf(*interact, "%[^:]:%[^:]:%s", &element, &action, &value)
		if n < 2 {
			fmt.Fprintln(os.Stderr, "glasssim: -interact wants elementID:action[:value]")
			os.Exit(2)
		}
		if err := dev.Interact(element, action, value); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Printf("interaction reported: %s %s %q\n", element, action, value)
	}

	// Startup telemetry, then the periodic loop.
	if err := dev.Telemetry(*battery, false); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("telemetry: battery=%d%%\n", *battery)

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			fmt.Println("glasssim: shutting down")
			return
		case <-ticker.C:
			if err := dev.Telemetry(*battery, false); err != nil {
				fmt.Fprintf(os.Stderr, "telemetry: %v\n", err)
			}
		case ev, ok := <-dev.Events():
			if !ok {
				if err := dev.Err(); err != nil {
					fmt.Fprintln(os.Stderr, err)
					os.Exit(1)
				}
				return
			}
			fmt.Printf("event %s %s\n", ev.Kind, ev.Raw)
		case <-time.After(5 * time.Second):
			if *once {
				fmt.Println("glasssim: cycle complete")
				return
			}
		}
	}
}
