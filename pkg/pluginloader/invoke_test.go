package pluginloader

import (
	"testing"

	"github.com/qomos-w/sporemind/pkg/pluginhost"
)

func TestInvokeRejectsNegativeCapacity(t *testing.T) {
	lib := &Library{}
	if _, err := lib.Invoke("PluginInvoke", nil, -1); err == nil {
		t.Fatal("expected negative capacity error")
	}
}

func TestRetryCapacity(t *testing.T) {
	tests := []struct {
		name            string
		status          int32
		responseLen     uintptr
		currentCapacity int
		wantRetry       int  // expected needed capacity (0 = no retry)
		wantErr         bool // expect error (invalid signal)
	}{
		{
			name:            "success status, no retry",
			status:          pluginhost.StatusOK,
			responseLen:     0,
			currentCapacity: 1024,
			wantRetry:       0,
			wantErr:         false,
		},
		{
			name:            "generic error, no retry",
			status:          -1,
			responseLen:     0,
			currentCapacity: 1024,
			wantRetry:       0,
			wantErr:         false,
		},
		{
			name:            "buffer too small, retry with needed",
			status:          pluginhost.StatusBufferTooSmall,
			responseLen:     4096,
			currentCapacity: 1024,
			wantRetry:       4096,
			wantErr:         false,
		},
		{
			name:            "buffer too small but needed equals capacity, error",
			status:          pluginhost.StatusBufferTooSmall,
			responseLen:     1024,
			currentCapacity: 1024,
			wantRetry:       0,
			wantErr:         true,
		},
		{
			name:            "buffer too small but needed below capacity, error",
			status:          pluginhost.StatusBufferTooSmall,
			responseLen:     512,
			currentCapacity: 1024,
			wantRetry:       0,
			wantErr:         true,
		},
		{
			name:            "buffer too small, exceeds max, error",
			status:          pluginhost.StatusBufferTooSmall,
			responseLen:     uintptr(pluginhost.MaxResponseBytes + 1),
			currentCapacity: 1024,
			wantRetry:       0,
			wantErr:         true,
		},
		{
			name:            "buffer too small, needed at max boundary, retry",
			status:          pluginhost.StatusBufferTooSmall,
			responseLen:     uintptr(pluginhost.MaxResponseBytes),
			currentCapacity: 1024,
			wantRetry:       pluginhost.MaxResponseBytes,
			wantErr:         false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := retryCapacity(tt.status, tt.responseLen, tt.currentCapacity)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.wantRetry {
				t.Fatalf("retry capacity = %d, want %d", got, tt.wantRetry)
			}
		})
	}
}
