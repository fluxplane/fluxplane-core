package browser

import (
	"context"
	"testing"

	browserapi "github.com/fluxplane/fluxplane-browser"
)

func TestPluginLazilyCreatesAndReusesManager(t *testing.T) {
	calls := 0
	manager := fakeManager{}
	plugin := New(Config{
		Factory: func(context.Context) (browserapi.Manager, error) {
			calls++
			return manager, nil
		},
	})

	first, failed := plugin.browser(context.Background())
	if first == nil || failed.Status == "failed" {
		t.Fatalf("first manager = %v, failed = %#v", first, failed)
	}
	second, failed := plugin.browser(context.Background())
	if second == nil || failed.Status == "failed" {
		t.Fatalf("second manager = %v, failed = %#v", second, failed)
	}
	if calls != 1 {
		t.Fatalf("factory calls = %d, want 1", calls)
	}
}

type fakeManager struct{}

func (fakeManager) Open(context.Context, browserapi.OpenRequest) (browserapi.OpenResult, error) {
	return browserapi.OpenResult{}, nil
}
func (fakeManager) Navigate(context.Context, browserapi.SessionRequest) (browserapi.PageResult, error) {
	return browserapi.PageResult{}, nil
}
func (fakeManager) Click(context.Context, browserapi.SelectorRequest) (browserapi.PageResult, error) {
	return browserapi.PageResult{}, nil
}
func (fakeManager) Type(context.Context, browserapi.TypeRequest) (browserapi.PageResult, error) {
	return browserapi.PageResult{}, nil
}
func (fakeManager) Select(context.Context, browserapi.SelectRequest) (browserapi.PageResult, error) {
	return browserapi.PageResult{}, nil
}
func (fakeManager) Read(context.Context, browserapi.ReadRequest) (browserapi.ReadResult, error) {
	return browserapi.ReadResult{}, nil
}
func (fakeManager) Screenshot(context.Context, browserapi.SessionRequest) (browserapi.Artifact, error) {
	return browserapi.Artifact{}, nil
}
func (fakeManager) Evaluate(context.Context, browserapi.EvaluateRequest) (browserapi.EvaluateResult, error) {
	return browserapi.EvaluateResult{}, nil
}
func (fakeManager) Wait(context.Context, browserapi.WaitRequest) (browserapi.PageResult, error) {
	return browserapi.PageResult{}, nil
}
func (fakeManager) Scroll(context.Context, browserapi.ScrollRequest) (browserapi.PageResult, error) {
	return browserapi.PageResult{}, nil
}
func (fakeManager) Hover(context.Context, browserapi.SelectorRequest) (browserapi.PageResult, error) {
	return browserapi.PageResult{}, nil
}
func (fakeManager) Back(context.Context, browserapi.SessionRequest) (browserapi.PageResult, error) {
	return browserapi.PageResult{}, nil
}
func (fakeManager) Forward(context.Context, browserapi.SessionRequest) (browserapi.PageResult, error) {
	return browserapi.PageResult{}, nil
}
func (fakeManager) PDF(context.Context, browserapi.SessionRequest) (browserapi.Artifact, error) {
	return browserapi.Artifact{}, nil
}
func (fakeManager) Close(context.Context, browserapi.SessionRequest) error { return nil }
