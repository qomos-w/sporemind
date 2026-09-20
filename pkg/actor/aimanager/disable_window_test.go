package aimanager

import (
	"testing"
	"time"

	"github.com/qomos-w/sporemind/pkg/domain"
	"github.com/qomos-w/sporemind/pkg/testutil"
)

func newDisableWindowActor() *Actor {
	return &Actor{
		actorID: "test-aimanager",
		store:   &memStore{data: make(map[string][]byte)},
		Providers: []domain.Provider{
			{
				Name:           "openai",
				Kind:           "openai",
				Endpoint:       "https://openai",
				AuthToken:      "sk-test",
				Models:         []domain.ProviderModel{{Name: "gpt-4o"}},
				DisableWindows: []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00"}},
			},
		},
		persistedHealth: make(map[string]persistedHealthEntry),
	}
}

func TestProviderConfigure_EmptyWindowsClearsScheduleNotProvider(t *testing.T) {
	a := newDisableWindowActor()
	ctx := testutil.AdminCtx(testutil.GenActorID())

	_, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{
		Name:      "openai",
		Kind:      "openai",
		Endpoint:  "https://openai",
		AuthToken: "sk-test",
		Models:    []domain.ProviderModel{{Name: "gpt-4o"}},
	})
	if err != nil {
		t.Fatalf("configure failed: %v", err)
	}

	if len(a.Providers) != 1 || a.Providers[0].Name != "openai" {
		t.Fatalf("provider must survive window-clearing update, got %+v", a.Providers)
	}
	if len(a.Providers[0].DisableWindows) != 0 {
		t.Fatalf("disable windows must be cleared, got %+v", a.Providers[0].DisableWindows)
	}
}

func TestProviderConfigure_AllFieldsEmptyStillDeletes(t *testing.T) {
	a := newDisableWindowActor()
	ctx := testutil.AdminCtx(testutil.GenActorID())

	_, err := a.handleProviderConfigure(ctx, domain.AIManagerProviderConfigureReq{Name: "openai"})
	if err != nil {
		t.Fatalf("configure failed: %v", err)
	}

	if len(a.Providers) != 0 {
		t.Fatalf("provider must be deleted when all fields are empty, got %+v", a.Providers)
	}
}

func TestProviderDisableDeadline_WindowUnion(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, time.August, 16, 0, 0, 0, 0, loc)

	tests := []struct {
		name    string
		windows []domain.ProviderDisableWindow
		now     time.Time
		want    time.Time
	}{
		{
			name:    "outside simple window",
			windows: []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00"}},
			now:     day.Add(8 * time.Hour),
		},
		{
			name:    "simple window end",
			windows: []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00"}},
			now:     day.Add(17 * time.Hour),
		},
		{
			name: "overlapping windows merge",
			windows: []domain.ProviderDisableWindow{
				{Start: "09:00", End: "12:00"},
				{Start: "11:00", End: "15:00"},
			},
			now:  day.Add(10 * time.Hour),
			want: day.Add(15 * time.Hour),
		},
		{
			name: "cross midnight late segment",
			windows: []domain.ProviderDisableWindow{
				{Start: "22:00", End: "06:00"},
			},
			now:  day.Add(23 * time.Hour),
			want: day.Add(24 * time.Hour).Add(6 * time.Hour),
		},
		{
			name: "cross midnight early segment",
			windows: []domain.ProviderDisableWindow{
				{Start: "22:00", End: "06:00"},
			},
			now:  day.Add(2 * time.Hour),
			want: day.Add(6 * time.Hour),
		},
		{
			name: "cross midnight union continues through overlap",
			windows: []domain.ProviderDisableWindow{
				{Start: "22:00", End: "02:00"},
				{Start: "01:00", End: "06:00"},
			},
			now:  day.Add(23 * time.Hour),
			want: day.Add(24 * time.Hour).Add(6 * time.Hour),
		},
		{
			name:    "all day start equals end",
			windows: []domain.ProviderDisableWindow{{Start: "12:00", End: "12:00"}},
			now:     day.Add(12 * time.Hour),
			want:    day.Add(24 * time.Hour),
		},
		{
			name:    "all day sunday only",
			windows: []domain.ProviderDisableWindow{{Start: "00:00", End: "00:00", Days: []int32{7}}},
			now:     day.Add(10 * time.Hour),
			want:    day.Add(24 * time.Hour),
		},
		{
			name:    "all day sunday only inactive on monday",
			windows: []domain.ProviderDisableWindow{{Start: "00:00", End: "00:00", Days: []int32{7}}},
			now:     day.Add(24 * time.Hour).Add(10 * time.Hour),
		},
		{
			name:    "all day no days regression still disables every day",
			windows: []domain.ProviderDisableWindow{{Start: "00:00", End: "00:00"}},
			now:     day.Add(10 * time.Hour),
			want:    day.Add(24 * time.Hour),
		},
		{
			name: "all day sunday merged with regular overnight window",
			windows: []domain.ProviderDisableWindow{
				{Start: "00:00", End: "00:00", Days: []int32{7}},
				{Start: "00:00", End: "06:00"},
			},
			now:  day.Add(10 * time.Hour),
			want: day.Add(24 * time.Hour).Add(6 * time.Hour),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := providerDisableDeadline(tt.windows, tt.now)
			if tt.want.IsZero() {
				if got != 0 {
					t.Fatalf("providerDisableDeadline() = %v, want no deadline", time.Unix(got, 0).In(loc))
				}
				return
			}
			if got != tt.want.Unix() {
				t.Fatalf("providerDisableDeadline() = %v, want %v", time.Unix(got, 0).In(loc), tt.want)
			}
		})
	}
}

func TestProviderDisableDeadline_HalfOpenBoundary(t *testing.T) {
	loc := time.UTC
	day := time.Date(2026, time.August, 16, 0, 0, 0, 0, loc)
	window := []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00"}}

	if got := providerDisableDeadline(window, day.Add(9*time.Hour-time.Second)); got != 0 {
		t.Fatalf("before start returned deadline %v", time.Unix(got, 0))
	}
	if got := providerDisableDeadline(window, day.Add(9*time.Hour)); got != day.Add(17*time.Hour).Unix() {
		t.Fatalf("at start returned %v, want %v", time.Unix(got, 0), day.Add(17*time.Hour))
	}
	if got := providerDisableDeadline(window, day.Add(17*time.Hour)); got != 0 {
		t.Fatalf("at end returned deadline %v", time.Unix(got, 0))
	}
}

func TestDisableWindowsEqual_WithDays(t *testing.T) {
	a := []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00", Days: []int32{1, 2, 3}}}
	b := []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00", Days: []int32{1, 2, 3}}}
	if !disableWindowsEqual(a, b) {
		t.Fatal("expected equal windows with identical days")
	}
	c := []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00", Days: []int32{1, 2}}}
	if disableWindowsEqual(a, c) {
		t.Fatal("expected different days to make windows unequal")
	}
	d := []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00"}}
	if disableWindowsEqual(a, d) {
		t.Fatal("expected absent days to make windows unequal")
	}
}

func TestProviderDisableDeadline_WeekdayFilter(t *testing.T) {
	loc := time.UTC
	sunday := time.Date(2026, time.August, 16, 0, 0, 0, 0, loc)
	monday := time.Date(2026, time.August, 17, 0, 0, 0, 0, loc)

	tests := []struct {
		name    string
		windows []domain.ProviderDisableWindow
		now     time.Time
		want    time.Time
	}{
		{
			name:    "allowed weekday",
			windows: []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00", Days: []int32{7}}},
			now:     sunday.Add(10 * time.Hour),
			want:    sunday.Add(17 * time.Hour),
		},
		{
			name:    "disallowed weekday",
			windows: []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00", Days: []int32{1, 2, 3, 4, 5}}},
			now:     sunday.Add(10 * time.Hour),
		},
		{
			name:    "empty days means every day",
			windows: []domain.ProviderDisableWindow{{Start: "09:00", End: "17:00", Days: []int32{}}},
			now:     sunday.Add(10 * time.Hour),
			want:    sunday.Add(17 * time.Hour),
		},
		{
			name:    "cross midnight sunday evening only sunday allowed",
			windows: []domain.ProviderDisableWindow{{Start: "22:00", End: "06:00", Days: []int32{7}}},
			now:     sunday.Add(23 * time.Hour),
			want:    monday,
		},
		{
			name:    "cross midnight sunday evening needs both days",
			windows: []domain.ProviderDisableWindow{{Start: "22:00", End: "06:00", Days: []int32{7, 1}}},
			now:     sunday.Add(23 * time.Hour),
			want:    monday.Add(6 * time.Hour),
		},
		{
			name:    "cross midnight monday morning needs both days",
			windows: []domain.ProviderDisableWindow{{Start: "22:00", End: "06:00", Days: []int32{7, 1}}},
			now:     monday.Add(1 * time.Hour),
			want:    monday.Add(6 * time.Hour),
		},
		{
			name:    "cross midnight monday morning allowed by monday weekday",
			windows: []domain.ProviderDisableWindow{{Start: "22:00", End: "06:00", Days: []int32{1}}},
			now:     monday.Add(1 * time.Hour),
			want:    monday.Add(6 * time.Hour),
		},
		{
			name:    "cross midnight sunday evening disallowed without sunday",
			windows: []domain.ProviderDisableWindow{{Start: "22:00", End: "06:00", Days: []int32{1}}},
			now:     sunday.Add(23 * time.Hour),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := providerDisableDeadline(tt.windows, tt.now)
			if tt.want.IsZero() {
				if got != 0 {
					t.Fatalf("providerDisableDeadline() = %v, want no deadline", time.Unix(got, 0).In(loc))
				}
				return
			}
			if got != tt.want.Unix() {
				t.Fatalf("providerDisableDeadline() = %v, want %v", time.Unix(got, 0).In(loc), tt.want)
			}
		})
	}
}
