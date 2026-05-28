package clock

import (
	"context"
	"strings"
	"testing"
	"time"

	corecontext "github.com/fluxplane/fluxplane-core/core/context"
)

func TestContextProvider_CachesWithin60s(t *testing.T) {
	base := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)
	clock := &fakeClock{t: base}
	p := &ContextProvider{Now: clock.Now, StartAt: base.Add(-90 * time.Second)}

	first, err := p.Build(context.Background(), corecontext.Request{})
	if err != nil {
		t.Fatalf("first build: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("want 1 block, got %d", len(first))
	}

	clock.advance(30 * time.Second)
	second, err := p.Build(context.Background(), corecontext.Request{})
	if err != nil {
		t.Fatalf("second build: %v", err)
	}
	if second[0].Content != first[0].Content {
		t.Fatalf("expected cached content, got:\nfirst:  %q\nsecond: %q", first[0].Content, second[0].Content)
	}

	clock.advance(31 * time.Second) // total +61s
	third, err := p.Build(context.Background(), corecontext.Request{})
	if err != nil {
		t.Fatalf("third build: %v", err)
	}
	if third[0].Content == first[0].Content {
		t.Fatalf("expected refreshed content after 61s; still got %q", third[0].Content)
	}
}

func TestContextProvider_IncludesUptime(t *testing.T) {
	start := time.Date(2026, 5, 28, 9, 0, 0, 0, time.UTC)
	now := start.Add(2*time.Hour + 30*time.Minute)
	p := &ContextProvider{Now: func() time.Time { return now }, StartAt: start}

	blocks, err := p.Build(context.Background(), corecontext.Request{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(blocks[0].Content, "Uptime: 2h30m") {
		t.Fatalf("expected uptime in content, got %q", blocks[0].Content)
	}
	if !strings.Contains(blocks[0].Content, "Current time: 2026-05-28T11:30:00Z") {
		t.Fatalf("expected current UTC in content, got %q", blocks[0].Content)
	}
}

func TestContextProvider_LocalTZ(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)
	p := &ContextProvider{Now: func() time.Time { return now }, TZ: "Europe/Berlin", StartAt: now}

	blocks, err := p.Build(context.Background(), corecontext.Request{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(blocks[0].Content, "(Europe/Berlin:") {
		t.Fatalf("expected local TZ in content, got %q", blocks[0].Content)
	}
}

func TestFormatAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{45 * time.Second, "45s"},
		{2 * time.Minute, "2m0s"},
		{2*time.Minute + 15*time.Second, "2m15s"},
		{3*time.Hour + 5*time.Minute, "3h5m"},
		{49 * time.Hour, "2d1h0m"},
		{-time.Second, "0s"},
	}
	for _, c := range cases {
		if got := formatAge(c.d); got != c.want {
			t.Errorf("formatAge(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}

type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }
