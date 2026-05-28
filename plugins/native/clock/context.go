package clock

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	corecontext "github.com/fluxplane/fluxplane-core/core/context"
)

const cacheTTL = 60 * time.Second

// ContextProvider injects a "current time" block on each turn, refreshing
// at most once per minute. The block also reports uptime since StartAt.
type ContextProvider struct {
	Now     func() time.Time
	TZ      string
	StartAt time.Time

	mu        sync.Mutex
	lastAt    time.Time
	lastBlock corecontext.Block
}

func (p *ContextProvider) Spec() corecontext.ProviderSpec {
	return corecontext.ProviderSpec{
		Name:             ContextProviderName,
		Description:      "Current wall-clock time.",
		Kinds:            []corecontext.BlockKind{corecontext.BlockData},
		DefaultPlacement: corecontext.PlacementSystem,
	}
}

func (p *ContextProvider) Build(_ context.Context, _ corecontext.Request) ([]corecontext.Block, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := p.now()
	if !p.lastAt.IsZero() && now.Sub(p.lastAt) < cacheTTL && p.lastBlock.ID != "" {
		return []corecontext.Block{p.lastBlock}, nil
	}

	p.lastBlock = corecontext.Block{
		ID:        "now",
		Provider:  ContextProviderName,
		Kind:      corecontext.BlockData,
		Placement: corecontext.PlacementSystem,
		Title:     "Time",
		Content:   renderTime(now, p.TZ, p.StartAt),
		MediaType: "text/plain",
		Priority:  90,
		Freshness: corecontext.FreshnessDynamic,
	}
	p.lastAt = now
	return []corecontext.Block{p.lastBlock}, nil
}

func (p *ContextProvider) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func renderTime(now time.Time, tz string, startAt time.Time) string {
	utc := now.UTC().Format(time.RFC3339)
	var b strings.Builder
	b.WriteString("Current time: ")
	b.WriteString(utc)
	if tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil {
			fmt.Fprintf(&b, " (%s: %s)", tz, now.In(loc).Format(time.RFC3339))
		} else {
			fmt.Fprintf(&b, " (tz %q is unknown)", tz)
		}
	}
	if !startAt.IsZero() {
		fmt.Fprintf(&b, "\nUptime: %s (since %s)", formatAge(now.Sub(startAt)), startAt.UTC().Format(time.RFC3339))
	}
	return b.String()
}

func formatAge(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	days := int(d / (24 * time.Hour))
	d -= time.Duration(days) * 24 * time.Hour
	hours := int(d / time.Hour)
	d -= time.Duration(hours) * time.Hour
	minutes := int(d / time.Minute)
	d -= time.Duration(minutes) * time.Minute
	seconds := int(d / time.Second)
	switch {
	case days > 0:
		return fmt.Sprintf("%dd%dh%dm", days, hours, minutes)
	case hours > 0:
		return fmt.Sprintf("%dh%dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm%ds", minutes, seconds)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}
