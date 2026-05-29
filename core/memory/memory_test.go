package memory

import (
	"testing"

	"github.com/fluxplane/fluxplane-event"
)

func TestRegisterEvents(t *testing.T) {
	registry := event.NewRegistry()

	err := RegisterEvents(registry)
	if err != nil {
		t.Fatalf("RegisterEvents: %v", err)
	}

	// Verify each event type was registered by trying to decode an empty payload
	for _, sample := range []event.Event{
		Memorized{},
		Forgotten{},
		Organized{},
	} {
		_, found, err := registry.TryDecode(sample.EventName(), []byte("{}"))
		if err != nil {
			t.Errorf("TryDecode for %q: %v", sample.EventName(), err)
			continue
		}
		if !found {
			t.Errorf("RegisterEvents: event %q not registered", sample.EventName())
		}
	}
}

func TestRegisterEventsNilRegistry(t *testing.T) {
	err := RegisterEvents(nil)
	if err == nil {
		t.Fatal("RegisterEvents(nil) error = nil, want error")
	}
	if err.Error() != "memory: event registry is nil" {
		t.Errorf("RegisterEvents(nil) error = %q, want %q", err.Error(), "memory: event registry is nil")
	}
}

func TestEventNames(t *testing.T) {
	if got, want := EventMemorizedName, event.Name("memory.memorized"); got != want {
		t.Errorf("EventMemorizedName = %q, want %q", got, want)
	}
	if got, want := EventForgottenName, event.Name("memory.forgotten"); got != want {
		t.Errorf("EventForgottenName = %q, want %q", got, want)
	}
	if got, want := EventOrganizedName, event.Name("memory.organized"); got != want {
		t.Errorf("EventOrganizedName = %q, want %q", got, want)
	}
}

func TestMemorizedEventName(t *testing.T) {
	m := Memorized{}
	if got, want := m.EventName(), EventMemorizedName; got != want {
		t.Errorf("Memorized.EventName() = %q, want %q", got, want)
	}
}

func TestForgottenEventName(t *testing.T) {
	f := Forgotten{}
	if got, want := f.EventName(), EventForgottenName; got != want {
		t.Errorf("Forgotten.EventName() = %q, want %q", got, want)
	}
}

func TestOrganizedEventName(t *testing.T) {
	o := Organized{}
	if got, want := o.EventName(), EventOrganizedName; got != want {
		t.Errorf("Organized.EventName() = %q, want %q", got, want)
	}
}

func TestMemoryConstants(t *testing.T) {
	// Verify Kind constants
	if KindFact != "fact" {
		t.Errorf("KindFact = %q, want %q", KindFact, "fact")
	}
	if KindPreference != "preference" {
		t.Errorf("KindPreference = %q, want %q", KindPreference, "preference")
	}
	if KindInstruction != "instruction" {
		t.Errorf("KindInstruction = %q, want %q", KindInstruction, "instruction")
	}
	if KindDecision != "decision" {
		t.Errorf("KindDecision = %q, want %q", KindDecision, "decision")
	}
	if KindProcedure != "procedure" {
		t.Errorf("KindProcedure = %q, want %q", KindProcedure, "procedure")
	}
	if KindSummary != "summary" {
		t.Errorf("KindSummary = %q, want %q", KindSummary, "summary")
	}
	if KindReference != "reference" {
		t.Errorf("KindReference = %q, want %q", KindReference, "reference")
	}

	// Verify Status constants
	if StatusActive != "active" {
		t.Errorf("StatusActive = %q, want %q", StatusActive, "active")
	}
	if StatusArchived != "archived" {
		t.Errorf("StatusArchived = %q, want %q", StatusArchived, "archived")
	}
	if StatusForgotten != "forgotten" {
		t.Errorf("StatusForgotten = %q, want %q", StatusForgotten, "forgotten")
	}
	if StatusSuperseded != "superseded" {
		t.Errorf("StatusSuperseded = %q, want %q", StatusSuperseded, "superseded")
	}

	// Verify Visibility constants
	if VisibilityPrivateAgent != "private_agent" {
		t.Errorf("VisibilityPrivateAgent = %q, want %q", VisibilityPrivateAgent, "private_agent")
	}
	if VisibilityPrivateUser != "private_user" {
		t.Errorf("VisibilityPrivateUser = %q, want %q", VisibilityPrivateUser, "private_user")
	}
	if VisibilitySharedUserAgent != "shared_user_agent" {
		t.Errorf("VisibilitySharedUserAgent = %q, want %q", VisibilitySharedUserAgent, "shared_user_agent")
	}
	if VisibilityWorkspace != "workspace" {
		t.Errorf("VisibilityWorkspace = %q, want %q", VisibilityWorkspace, "workspace")
	}
	if VisibilityChannel != "channel" {
		t.Errorf("VisibilityChannel = %q, want %q", VisibilityChannel, "channel")
	}
	if VisibilityTenant != "tenant" {
		t.Errorf("VisibilityTenant = %q, want %q", VisibilityTenant, "tenant")
	}

	// Verify SubjectKind constants
	if SubjectUser != "user" {
		t.Errorf("SubjectUser = %q, want %q", SubjectUser, "user")
	}
	if SubjectAgent != "agent" {
		t.Errorf("SubjectAgent = %q, want %q", SubjectAgent, "agent")
	}
	if SubjectWorkspace != "workspace" {
		t.Errorf("SubjectWorkspace = %q, want %q", SubjectWorkspace, "workspace")
	}
	if SubjectSession != "session" {
		t.Errorf("SubjectSession = %q, want %q", SubjectSession, "session")
	}
	if SubjectThread != "thread" {
		t.Errorf("SubjectThread = %q, want %q", SubjectThread, "thread")
	}
	if SubjectChannel != "channel" {
		t.Errorf("SubjectChannel = %q, want %q", SubjectChannel, "channel")
	}
	if SubjectTask != "task" {
		t.Errorf("SubjectTask = %q, want %q", SubjectTask, "task")
	}
	if SubjectFile != "file" {
		t.Errorf("SubjectFile = %q, want %q", SubjectFile, "file")
	}
	if SubjectURL != "url" {
		t.Errorf("SubjectURL = %q, want %q", SubjectURL, "url")
	}
	if SubjectDatasource != "datasource" {
		t.Errorf("SubjectDatasource = %q, want %q", SubjectDatasource, "datasource")
	}
	if SubjectOther != "other" {
		t.Errorf("SubjectOther = %q, want %q", SubjectOther, "other")
	}

	// Verify ForgetMode constants
	if ForgetModeForget != "forget" {
		t.Errorf("ForgetModeForget = %q, want %q", ForgetModeForget, "forget")
	}
	if ForgetModeArchive != "archive" {
		t.Errorf("ForgetModeArchive = %q, want %q", ForgetModeArchive, "archive")
	}
	if ForgetModeExpire != "expire" {
		t.Errorf("ForgetModeExpire = %q, want %q", ForgetModeExpire, "expire")
	}

	// Verify OrganizeAction constants
	if OrganizeRetag != "retag" {
		t.Errorf("OrganizeRetag = %q, want %q", OrganizeRetag, "retag")
	}
	if OrganizeMerge != "merge" {
		t.Errorf("OrganizeMerge = %q, want %q", OrganizeMerge, "merge")
	}
	if OrganizeSupersede != "supersede" {
		t.Errorf("OrganizeSupersede = %q, want %q", OrganizeSupersede, "supersede")
	}
	if OrganizeArchive != "archive" {
		t.Errorf("OrganizeArchive = %q, want %q", OrganizeArchive, "archive")
	}
	if OrganizeSummarize != "summarize" {
		t.Errorf("OrganizeSummarize = %q, want %q", OrganizeSummarize, "summarize")
	}
}

func TestSourceName(t *testing.T) {
	if SourceName != "memory" {
		t.Errorf("SourceName = %q, want %q", SourceName, "memory")
	}
}

func TestItemEntity(t *testing.T) {
	if ItemEntity != "memory.item" {
		t.Errorf("ItemEntity = %q, want %q", ItemEntity, "memory.item")
	}
}
