package codershell

import (
	"context"
	"testing"
)

func TestAppendInputStripsANSIEscapeSequences(t *testing.T) {
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: NewFakeClient(), CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	shell.AppendInput("\x1b[B\x1b[Bls\x1b[A")
	if got := shell.ActiveTab().InputBuffer; got != "ls" {
		t.Fatalf("InputBuffer = %q, want ls", got)
	}
}

func TestAppendInputStripsMouseEscapeSequences(t *testing.T) {
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: NewFakeClient(), CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	shell.AppendInput("ls\x1b[<64;15;8M\x1b[M`7% -la")
	shell.AppendInput("[<65;15;8M[M`7%")
	if got := shell.ActiveTab().InputBuffer; got != "ls -la" {
		t.Fatalf("InputBuffer = %q, want ls -la", got)
	}
}

func TestShellObjectCreatesSessionScopedTabs(t *testing.T) {
	client := NewFakeClient()
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: client, CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	first := shell.ActiveTab()
	if first == nil {
		t.Fatal("first active tab is nil")
	}
	firstID := first.ID
	shell.AppendInput("first")

	second, err := shell.NewTab(context.Background(), "/workspace/sub")
	if err != nil {
		t.Fatalf("NewTab() error = %v", err)
	}
	if second.ID == firstID {
		t.Fatalf("second tab reused session id %q", firstID)
	}
	shell.AppendInput("second")

	if !shell.SelectTab(0) {
		t.Fatal("SelectTab(0) failed")
	}
	if got := shell.ActiveTab().InputBuffer; got != "first" {
		t.Fatalf("first tab input = %q, want first", got)
	}
	if !shell.SelectTab(1) {
		t.Fatal("SelectTab(1) failed")
	}
	if got := shell.ActiveTab().InputBuffer; got != "second" {
		t.Fatalf("second tab input = %q, want second", got)
	}
}

func TestShellObjectSubmitUsesActiveSession(t *testing.T) {
	client := NewFakeClient()
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: client, CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	firstID := shell.ActiveTab().ID
	shell.AppendInput("whoami")
	if err := shell.SubmitActiveInput(context.Background()); err != nil {
		t.Fatalf("SubmitActiveInput() error = %v", err)
	}
	firstEvents := append([]TranscriptEvent(nil), shell.ActiveTab().Transcript...)

	if _, err := shell.NewTab(context.Background(), "/workspace"); err != nil {
		t.Fatalf("NewTab() error = %v", err)
	}
	secondID := shell.ActiveTab().ID
	shell.AppendInput("pwd")
	if err := shell.SubmitActiveInput(context.Background()); err != nil {
		t.Fatalf("SubmitActiveInput() second error = %v", err)
	}
	for _, event := range shell.ActiveTab().Transcript {
		if event.Kind == EventCommandStarted && event.SessionID != secondID {
			t.Fatalf("second command event session = %q, want %q", event.SessionID, secondID)
		}
	}

	if !shell.SelectTab(0) {
		t.Fatal("SelectTab(0) failed")
	}
	if shell.ActiveTab().ID != firstID {
		t.Fatalf("active first id = %q, want %q", shell.ActiveTab().ID, firstID)
	}
	if len(shell.ActiveTab().Transcript) != len(firstEvents) {
		t.Fatalf("first transcript length changed: got %d want %d", len(shell.ActiveTab().Transcript), len(firstEvents))
	}
	for _, event := range shell.ActiveTab().Transcript {
		if event.Kind == EventCommandStarted && event.SessionID != firstID {
			t.Fatalf("first command event session = %q, want %q", event.SessionID, firstID)
		}
	}
}

func TestShellObjectAskModeSubmitsAsk(t *testing.T) {
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: NewFakeClient(), CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	shell.ToggleInputMode()
	shell.AppendInput("what happened?")
	if err := shell.SubmitActiveInput(context.Background()); err != nil {
		t.Fatalf("SubmitActiveInput() error = %v", err)
	}
	found := false
	for _, event := range shell.ActiveTab().Transcript {
		if event.Kind == EventAskSubmitted {
			found = true
		}
	}
	if !found {
		t.Fatal("ask submission event not recorded")
	}
}

func TestShellObjectCDChangesOnlyActiveSession(t *testing.T) {
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: NewFakeClient(), CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	firstID := shell.ActiveTab().ID
	shell.AppendInput("cd src")
	if err := shell.SubmitActiveInput(context.Background()); err != nil {
		t.Fatalf("SubmitActiveInput(cd) error = %v", err)
	}
	if got := shell.ActiveTab().CWD; got != "/workspace/src" {
		t.Fatalf("first cwd = %q, want /workspace/src", got)
	}
	if _, err := shell.NewTab(context.Background(), "/workspace"); err != nil {
		t.Fatalf("NewTab() error = %v", err)
	}
	if got := shell.ActiveTab().CWD; got != "/workspace" {
		t.Fatalf("second cwd = %q, want /workspace", got)
	}
	if !shell.SelectTab(0) {
		t.Fatal("SelectTab(0) failed")
	}
	if shell.ActiveTab().ID != firstID || shell.ActiveTab().CWD != "/workspace/src" {
		t.Fatalf("first session changed: id=%q cwd=%q", shell.ActiveTab().ID, shell.ActiveTab().CWD)
	}
}

func TestShellObjectResourceSearchUsesActiveSessionCWD(t *testing.T) {
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: NewFakeClient(), CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	shell.AppendInput("cd pkg")
	if err := shell.SubmitActiveInput(context.Background()); err != nil {
		t.Fatalf("SubmitActiveInput(cd) error = %v", err)
	}
	results, err := shell.SearchResources(context.Background(), ResourceSearchQuery{Text: "coder", Limit: 3, Mention: true})
	if err != nil {
		t.Fatalf("SearchResources() error = %v", err)
	}
	if len(results) == 0 {
		t.Fatal("SearchResources() returned no results")
	}
}

func TestMentionHelpersPreserveStructuredSelection(t *testing.T) {
	query, ok := mentionQuery("ask @cod")
	if !ok || query != "cod" {
		t.Fatalf("mentionQuery() = %q, %v; want cod, true", query, ok)
	}
	input := replaceMentionFragment("ask @cod", ResourceSearchResult{Kind: ResourceAgent, ID: "coder", Label: "coder", InsertText: "@coder"})
	if input != "ask @coder " {
		t.Fatalf("replaceMentionFragment() = %q", input)
	}
}

func TestAskProjectionIncludesPriorTranscript(t *testing.T) {
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: NewFakeClient(), CWD: "/workspace", ContextPolicy: ContextPolicy{MaxEvents: 10, MaxBytes: 1024}})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	shell.AppendInput("echo hi")
	if err := shell.SubmitActiveInput(context.Background()); err != nil {
		t.Fatalf("SubmitActiveInput(command) error = %v", err)
	}
	shell.ToggleInputMode()
	shell.AppendInput("summarize")
	if err := shell.SubmitActiveInput(context.Background()); err != nil {
		t.Fatalf("SubmitActiveInput(ask) error = %v", err)
	}
	for _, event := range shell.ActiveTab().Transcript {
		if event.Kind == EventAskSubmitted {
			if event.Data["context_items"] == "0" || event.Data["context_items"] == "" {
				t.Fatalf("ask context_items = %q, want non-zero", event.Data["context_items"])
			}
			return
		}
	}
	t.Fatal("ask submission event not found")
}

func TestSlashCommandGoesThroughClient(t *testing.T) {
	shell, err := NewShellObject(context.Background(), ShellObjectOptions{Client: NewFakeClient(), CWD: "/workspace"})
	if err != nil {
		t.Fatalf("NewShellObject() error = %v", err)
	}
	shell.AppendInput("/help")
	if err := shell.SubmitActiveInput(context.Background()); err != nil {
		t.Fatalf("SubmitActiveInput(/help) error = %v", err)
	}
	for _, event := range shell.ActiveTab().Transcript {
		if event.Kind == EventSlashSubmitted {
			if event.SessionID != shell.ActiveTab().ID {
				t.Fatalf("slash session = %q, want %q", event.SessionID, shell.ActiveTab().ID)
			}
			return
		}
	}
	t.Fatal("slash submission event not found")
}

func TestProjectTranscriptBoundsEventsAndBytes(t *testing.T) {
	events := []TranscriptEvent{
		{Kind: EventCommandOutput, Summary: "one"},
		{Kind: EventCommandOutput, Summary: "two"},
		{Kind: EventCommandOutput, Summary: "three"},
	}
	items := ProjectTranscript(events, ContextPolicy{MaxEvents: 2, MaxBytes: 5})
	if len(items) != 2 {
		t.Fatalf("len(ProjectTranscript) = %d, want 2", len(items))
	}
	if items[0].Summary != "two" {
		t.Fatalf("first summary = %q, want two", items[0].Summary)
	}
	if len(items[1].Summary) > 2 {
		t.Fatalf("last summary was not byte-bounded: %q", items[1].Summary)
	}
}
