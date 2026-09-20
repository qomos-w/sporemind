package project

import "testing"

func TestHasScheduleData(t *testing.T) {
	if !hasScheduleData(map[string]any{"schedule": map[string]any{"cron": "0 9 * * *"}}) {
		t.Fatal("expected schedule data")
	}
	if hasScheduleData(map[string]any{"schedule": map[string]any{"cron": ""}}) {
		t.Fatal("expected empty schedule data")
	}
}
