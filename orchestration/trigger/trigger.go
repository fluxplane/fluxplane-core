// Package trigger runs configured daemon triggers through channel sessions.
package trigger

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	corechannel "github.com/fluxplane/engine/core/channel"
	corepolicy "github.com/fluxplane/engine/core/policy"
	corereaction "github.com/fluxplane/engine/core/reaction"
	coresession "github.com/fluxplane/engine/core/session"
	coretrigger "github.com/fluxplane/engine/core/trigger"
	clientapi "github.com/fluxplane/engine/orchestration/client"
)

// Config wires the daemon trigger host.
type Config struct {
	Client   clientapi.ChannelClient
	Specs    []coretrigger.Spec
	Caller   corepolicy.Caller
	Trust    corepolicy.Trust
	Channel  corechannel.Ref
	OnError  func(error)
	Now      func() time.Time
	NewRunID func(string) string
}

// Host runs configured daemon triggers.
type Host struct {
	client   clientapi.ChannelClient
	specs    []coretrigger.Spec
	caller   corepolicy.Caller
	trust    corepolicy.Trust
	channel  corechannel.Ref
	onError  func(error)
	now      func() time.Time
	newRunID func(string) string
}

// New returns a trigger host.
func New(cfg Config) (*Host, error) {
	if cfg.Client == nil {
		return nil, fmt.Errorf("trigger: channel client is nil")
	}
	if cfg.Now == nil {
		cfg.Now = func() time.Time { return time.Now().UTC() }
	}
	if cfg.NewRunID == nil {
		cfg.NewRunID = func(prefix string) string {
			return fmt.Sprintf("%s%d", prefix, cfg.Now().UnixNano())
		}
	}
	for i, spec := range cfg.Specs {
		if err := spec.Validate(); err != nil {
			return nil, fmt.Errorf("trigger: specs[%d]: %w", i, err)
		}
	}
	return &Host{
		client:   cfg.Client,
		specs:    append([]coretrigger.Spec(nil), cfg.Specs...),
		caller:   cfg.Caller,
		trust:    cfg.Trust,
		channel:  cfg.Channel,
		onError:  cfg.OnError,
		now:      cfg.Now,
		newRunID: cfg.NewRunID,
	}, nil
}

// Run starts all enabled scheduled triggers and blocks until ctx is canceled.
func (h *Host) Run(ctx context.Context) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("trigger: host is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var wg sync.WaitGroup
	for _, spec := range h.specs {
		spec := spec
		if spec.Disabled {
			continue
		}
		if spec.Kind == coretrigger.KindStartup {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := h.Fire(ctx, spec, h.now()); err != nil && ctx.Err() == nil {
					h.report(err)
				}
			}()
			continue
		}
		if spec.Kind != coretrigger.KindSchedule {
			continue
		}
		interval, err := time.ParseDuration(strings.TrimSpace(spec.Schedule.Every))
		if err != nil {
			return fmt.Errorf("trigger %q: parse schedule.every: %w", spec.Name, err)
		}
		if interval <= 0 {
			return fmt.Errorf("trigger %q: schedule.every must be positive", spec.Name)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case at := <-ticker.C:
					if err := h.Fire(ctx, spec, at.UTC()); err != nil && ctx.Err() == nil {
						h.report(err)
					}
				}
			}
		}()
	}
	<-ctx.Done()
	wg.Wait()
	return ctx.Err()
}

// Fire dispatches one trigger occurrence.
func (h *Host) Fire(ctx context.Context, spec coretrigger.Spec, at time.Time) error {
	if h == nil || h.client == nil {
		return fmt.Errorf("trigger: host is nil")
	}
	if err := spec.Validate(); err != nil {
		return err
	}
	if at.IsZero() {
		at = h.now()
	}
	session, err := h.client.Open(ctx, clientapi.OpenRequest{
		Session: coresession.Ref{Name: coresession.Name(spec.Session)},
		Conversation: corechannel.ConversationRef{
			ID: "trigger:" + spec.Name,
		},
		Metadata: map[string]string{
			"trigger": spec.Name,
			"source":  string(spec.Kind),
		},
	})
	if err != nil {
		return err
	}
	defer func() { _ = session.Close(ctx) }()
	submission := clientapi.NewSubmission().
		WithID(clientapi.RunID(h.newRunID("run_trigger_"))).
		WithCaller(h.caller).
		WithTrust(h.trust).
		WithTrigger(clientapi.Trigger{
			Name:    spec.Name,
			Source:  string(spec.Kind),
			Payload: map[string]any{"at": at.Format(time.RFC3339Nano)},
			Actions: append([]corereaction.Action(nil), spec.Actions...),
			Metadata: map[string]any{
				"scheduled_at": at.Format(time.RFC3339Nano),
			},
		})
	run, err := session.Submit(ctx, submission)
	if err != nil {
		return err
	}
	_, err = run.Wait(ctx)
	return err
}

func (h *Host) report(err error) {
	if h.onError != nil {
		h.onError(err)
	}
}
