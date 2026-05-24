package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	coredata "github.com/fluxplane/fluxplane-core/core/data"
	"github.com/fluxplane/fluxplane-core/core/event"
	corememory "github.com/fluxplane/fluxplane-core/core/memory"
	"github.com/fluxplane/fluxplane-core/core/thread"
	"github.com/fluxplane/fluxplane-core/core/user"
	"github.com/fluxplane/fluxplane-core/core/workspace"
	runtimedata "github.com/fluxplane/fluxplane-core/runtime/data"
	"github.com/fluxplane/fluxplane-core/runtime/eventstore"
)

func TestMemorySubjectDoesNotGrantAccess(t *testing.T) {
	ctx := context.Background()
	events := eventstore.NewMemoryStore()
	data := runtimedata.NewMemoryStore()
	store, err := NewStore(events, data)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}

	userSubject := corememory.Subject{Kind: corememory.SubjectUser, ID: "user-a"}
	agentScope := coredata.Scope{AgentID: "agent-a"}
	userScope := coredata.Scope{UserID: user.ID("user-a")}

	if _, err := store.Memorize(ctx, corememory.MemorizeRequest{
		Kind:        corememory.KindPreference,
		Visibility:  corememory.VisibilityPrivateAgent,
		Subjects:    []corememory.Subject{userSubject},
		AccessScope: agentScope,
		Title:       "User review style",
		Content:     "User prefers concise review comments.",
		Tags:        []string{"style"},
	}); err != nil {
		t.Fatalf("Memorize private agent memory: %v", err)
	}
	if _, err := store.Memorize(ctx, corememory.MemorizeRequest{
		Kind:        corememory.KindPreference,
		Visibility:  corememory.VisibilityPrivateUser,
		Subjects:    []corememory.Subject{userSubject},
		AccessScope: userScope,
		Title:       "User preference",
		Content:     "Remember this for the user.",
		Tags:        []string{"style"},
	}); err != nil {
		t.Fatalf("Memorize user memory: %v", err)
	}

	userResult, err := store.Retrieve(ctx, corememory.RetrieveRequest{
		AccessScope: userScope,
		Subjects:    []corememory.Subject{userSubject},
		Tags:        []string{"style"},
	})
	if err != nil {
		t.Fatalf("Retrieve user scope: %v", err)
	}
	if len(userResult.Memories) != 1 || userResult.Memories[0].Visibility != corememory.VisibilityPrivateUser {
		t.Fatalf("user memories = %#v, want only user-visible memory", userResult.Memories)
	}

	agentResult, err := store.Retrieve(ctx, corememory.RetrieveRequest{
		AccessScope: agentScope,
		Subjects:    []corememory.Subject{userSubject},
	})
	if err != nil {
		t.Fatalf("Retrieve agent scope: %v", err)
	}
	if len(agentResult.Memories) != 1 || agentResult.Memories[0].Visibility != corememory.VisibilityPrivateAgent {
		t.Fatalf("agent memories = %#v, want only agent-private memory", agentResult.Memories)
	}
}

func TestMemoryOperationsRejectEmptyAccessScope(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(eventstore.NewMemoryStore(), runtimedata.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if _, err := store.Memorize(ctx, corememory.MemorizeRequest{Content: "global write"}); err == nil {
		t.Fatal("Memorize with empty access scope succeeded")
	}
	if _, err := store.Retrieve(ctx, corememory.RetrieveRequest{}); err == nil {
		t.Fatal("Retrieve with empty access scope succeeded")
	}
	if _, err := store.Forget(ctx, corememory.ForgetRequest{IDs: []corememory.ID{"mem_1"}}); err == nil {
		t.Fatal("Forget with empty access scope succeeded")
	}
	if _, err := store.Organize(ctx, corememory.OrganizeRequest{IDs: []corememory.ID{"mem_1"}, Action: corememory.OrganizeArchive}); err == nil {
		t.Fatal("Organize with empty access scope succeeded")
	}
}

func TestRetrieveReplaysEventStreamWhenSnapshotStoreIsFresh(t *testing.T) {
	ctx := context.Background()
	events := eventstore.NewMemoryStore()
	first, err := NewStore(events, runtimedata.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore first: %v", err)
	}
	scope := coredata.Scope{UserID: user.ID("user-a")}
	if _, err := first.Memorize(ctx, corememory.MemorizeRequest{
		Kind:        corememory.KindFact,
		AccessScope: scope,
		Content:     "Replay marker alpha-7391 with viridian and salted plums.",
		Tags:        []string{"replay"},
	}); err != nil {
		t.Fatalf("Memorize: %v", err)
	}

	second, err := NewStore(events, runtimedata.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore second: %v", err)
	}
	result, err := second.Retrieve(ctx, corememory.RetrieveRequest{
		AccessScope: scope,
		Text:        "alpha-7391",
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(result.Memories) != 1 || !strings.Contains(result.Memories[0].Content, "viridian") {
		t.Fatalf("memories = %#v, want replayed memory", result.Memories)
	}
}

func TestRetrieveReplaysMultiDimensionalScopeFromSecondaryStream(t *testing.T) {
	ctx := context.Background()
	events := eventstore.NewMemoryStore()
	first, err := NewStore(events, runtimedata.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore first: %v", err)
	}
	scope := coredata.Scope{UserID: user.ID("user-a"), WorkspaceID: workspace.ID("workspace-a")}
	if _, err := first.Memorize(ctx, corememory.MemorizeRequest{
		Kind:        corememory.KindFact,
		AccessScope: scope,
		Content:     "Workspace replay marker beta-4209.",
	}); err != nil {
		t.Fatalf("Memorize: %v", err)
	}

	second, err := NewStore(events, runtimedata.NewMemoryStore())
	if err != nil {
		t.Fatalf("NewStore second: %v", err)
	}
	result, err := second.Retrieve(ctx, corememory.RetrieveRequest{
		AccessScope: coredata.Scope{WorkspaceID: workspace.ID("workspace-a")},
		Text:        "beta-4209",
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(result.Memories) != 1 || result.Memories[0].AccessScope.UserID != "user-a" {
		t.Fatalf("memories = %#v, want replayed multi-dimensional memory", result.Memories)
	}
}

func TestRetrieveAppliesLimitAfterMemoryFilters(t *testing.T) {
	ctx := context.Background()
	data := runtimedata.NewMemoryStore()
	store, err := NewStore(eventstore.NewMemoryStore(), data)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	scope := coredata.Scope{UserID: user.ID("user-a")}
	now := time.Now()
	var records []coredata.Record
	for i := 0; i < 25; i++ {
		records = append(records, RecordFromMemory(corememory.Memory{
			ID:          corememory.ID(fmtID("mem_%03d", i)),
			Kind:        corememory.KindFact,
			Status:      corememory.StatusArchived,
			AccessScope: scope,
			Content:     "Archived memory.",
			Provenance:  corememory.Provenance{UpdatedAt: now.Add(time.Duration(i) * time.Second)},
		}))
	}
	records = append(records, RecordFromMemory(corememory.Memory{
		ID:          "mem_999",
		Kind:        corememory.KindFact,
		Status:      corememory.StatusActive,
		AccessScope: scope,
		Content:     "Active marker gamma-5127.",
		Provenance:  corememory.Provenance{UpdatedAt: now.Add(30 * time.Second)},
	}))
	if err := data.UpsertRecords(ctx, records...); err != nil {
		t.Fatalf("UpsertRecords: %v", err)
	}

	result, err := store.Retrieve(ctx, corememory.RetrieveRequest{
		AccessScope: scope,
		Limit:       1,
	})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(result.Memories) != 1 || !strings.Contains(result.Memories[0].Content, "gamma-5127") {
		t.Fatalf("memories = %#v, want active memory after post-filter limit", result.Memories)
	}
}

func TestForgetTombstonesSnapshotAndPreservesEvent(t *testing.T) {
	ctx := context.Background()
	events := eventstore.NewMemoryStore()
	data := runtimedata.NewMemoryStore()
	store, err := NewStore(events, data)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	scope := coredata.Scope{ThreadID: thread.ID("thread-a")}
	stored, err := store.Memorize(ctx, corememory.MemorizeRequest{
		Kind:        corememory.KindFact,
		AccessScope: scope,
		Content:     "Temporary fact.",
	})
	if err != nil {
		t.Fatalf("Memorize: %v", err)
	}

	forgotten, err := store.Forget(ctx, corememory.ForgetRequest{
		AccessScope: scope,
		IDs:         []corememory.ID{stored.Memory.ID},
		Reason:      "stale",
	})
	if err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if len(forgotten.Affected) != 1 || forgotten.Affected[0] != stored.Memory.ID {
		t.Fatalf("affected = %#v, want %s", forgotten.Affected, stored.Memory.ID)
	}

	active, err := store.Retrieve(ctx, corememory.RetrieveRequest{AccessScope: scope})
	if err != nil {
		t.Fatalf("Retrieve active: %v", err)
	}
	if len(active.Memories) != 0 {
		t.Fatalf("active memories = %#v, want none", active.Memories)
	}

	withForgotten, err := store.Retrieve(ctx, corememory.RetrieveRequest{AccessScope: scope, IncludeForgotten: true})
	if err != nil {
		t.Fatalf("Retrieve forgotten: %v", err)
	}
	if len(withForgotten.Memories) != 1 || withForgotten.Memories[0].Status != corememory.StatusForgotten || withForgotten.Memories[0].Content != "" {
		t.Fatalf("forgotten memories = %#v, want tombstone without content", withForgotten.Memories)
	}

	records, err := events.Load(ctx, StreamID(scope), event.LoadOptions{})
	if err != nil {
		t.Fatalf("Load events: %v", err)
	}
	if len(records) != 2 || records[0].Record.Name != corememory.EventMemorizedName || records[1].Record.Name != corememory.EventForgottenName {
		t.Fatalf("event records = %#v, want memorized then forgotten", records)
	}
}

func fmtID(format string, value int) string {
	return fmt.Sprintf(format, value)
}

func TestOrganizeRetagsMemory(t *testing.T) {
	ctx := context.Background()
	events := eventstore.NewMemoryStore()
	data := runtimedata.NewMemoryStore()
	store, err := NewStore(events, data)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	scope := coredata.Scope{SessionID: "session-a"}
	stored, err := store.Memorize(ctx, corememory.MemorizeRequest{
		Kind:        corememory.KindDecision,
		AccessScope: scope,
		Content:     "Use the memory data store for query snapshots.",
		Tags:        []string{"old"},
	})
	if err != nil {
		t.Fatalf("Memorize: %v", err)
	}
	if _, err := store.Organize(ctx, corememory.OrganizeRequest{
		AccessScope: scope,
		IDs:         []corememory.ID{stored.Memory.ID},
		Action:      corememory.OrganizeRetag,
		Tags:        []string{"storage", "memory"},
	}); err != nil {
		t.Fatalf("Organize: %v", err)
	}
	result, err := store.Retrieve(ctx, corememory.RetrieveRequest{AccessScope: scope, Tags: []string{"storage"}})
	if err != nil {
		t.Fatalf("Retrieve: %v", err)
	}
	if len(result.Memories) != 1 || result.Memories[0].Tags[0] != "memory" || result.Memories[0].Tags[1] != "storage" {
		t.Fatalf("memories = %#v, want retagged memory", result.Memories)
	}
}
