package memory

import (
	"time"

	coredata "github.com/fluxplane/agentruntime/core/data"
	"github.com/fluxplane/agentruntime/core/event"
	"github.com/fluxplane/agentruntime/core/policy"
)

const (
	SourceName coredata.SourceName = "memory"
	ItemEntity coredata.EntityType = "memory.item"
)

type ID string

type Kind string

const (
	KindFact        Kind = "fact"
	KindPreference  Kind = "preference"
	KindInstruction Kind = "instruction"
	KindDecision    Kind = "decision"
	KindProcedure   Kind = "procedure"
	KindSummary     Kind = "summary"
	KindReference   Kind = "reference"
)

type Status string

const (
	StatusActive     Status = "active"
	StatusArchived   Status = "archived"
	StatusForgotten  Status = "forgotten"
	StatusSuperseded Status = "superseded"
)

type Visibility string

const (
	VisibilityPrivateAgent    Visibility = "private_agent"
	VisibilityPrivateUser     Visibility = "private_user"
	VisibilitySharedUserAgent Visibility = "shared_user_agent"
	VisibilityWorkspace       Visibility = "workspace"
	VisibilityChannel         Visibility = "channel"
	VisibilityTenant          Visibility = "tenant"
)

type SubjectKind string

const (
	SubjectUser       SubjectKind = "user"
	SubjectAgent      SubjectKind = "agent"
	SubjectWorkspace  SubjectKind = "workspace"
	SubjectSession    SubjectKind = "session"
	SubjectThread     SubjectKind = "thread"
	SubjectChannel    SubjectKind = "channel"
	SubjectTask       SubjectKind = "task"
	SubjectFile       SubjectKind = "file"
	SubjectURL        SubjectKind = "url"
	SubjectDatasource SubjectKind = "datasource"
	SubjectOther      SubjectKind = "other"
)

type Subject struct {
	Kind        SubjectKind       `json:"kind" jsonschema:"description=What kind of thing this memory is about."`
	ID          string            `json:"id,omitempty" jsonschema:"description=Stable subject id when one is available."`
	Name        string            `json:"name,omitempty" jsonschema:"description=Human-readable subject label."`
	Ref         coredata.Ref      `json:"ref,omitempty" jsonschema:"description=Datasource or data-store record reference when the subject is a record."`
	Path        string            `json:"path,omitempty" jsonschema:"description=File path when the subject is a file."`
	URL         string            `json:"url,omitempty" jsonschema:"description=URL when the subject is web content."`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type SourceRef struct {
	Kind        string       `json:"kind,omitempty" jsonschema:"description=Source kind such as file, url, datasource, operation, thread, session, or user."`
	ID          string       `json:"id,omitempty"`
	Name        string       `json:"name,omitempty"`
	Ref         coredata.Ref `json:"ref,omitempty"`
	Path        string       `json:"path,omitempty"`
	URL         string       `json:"url,omitempty"`
	Description string       `json:"description,omitempty"`
}

type Provenance struct {
	ActorKind   string      `json:"actor_kind,omitempty"`
	ActorID     string      `json:"actor_id,omitempty"`
	RequesterID string      `json:"requester_id,omitempty"`
	SessionID   string      `json:"session_id,omitempty"`
	ThreadID    string      `json:"thread_id,omitempty"`
	OperationID string      `json:"operation_id,omitempty"`
	SourceRefs  []SourceRef `json:"source_refs,omitempty"`
	CreatedAt   time.Time   `json:"created_at,omitempty"`
	UpdatedAt   time.Time   `json:"updated_at,omitempty"`
}

type Memory struct {
	ID          ID                 `json:"id"`
	Kind        Kind               `json:"kind"`
	Status      Status             `json:"status,omitempty"`
	Visibility  Visibility         `json:"visibility,omitempty"`
	Subjects    []Subject          `json:"subjects,omitempty"`
	AccessScope coredata.Scope     `json:"access_scope"`
	Title       string             `json:"title,omitempty"`
	Content     string             `json:"content"`
	Data        map[string]any     `json:"data,omitempty"`
	Tags        []string           `json:"tags,omitempty"`
	BlobRefs    []coredata.BlobRef `json:"blob_refs,omitempty"`
	Supersedes  []ID               `json:"supersedes,omitempty"`
	Sensitivity policy.Sensitivity `json:"sensitivity,omitempty"`
	ExpiresAt   time.Time          `json:"expires_at,omitempty"`
	Provenance  Provenance         `json:"provenance,omitempty"`
}

type MemorizeRequest struct {
	ID          ID                 `json:"id,omitempty" jsonschema:"description=Optional stable memory id for replacing or upserting a known memory."`
	Kind        Kind               `json:"kind" jsonschema:"description=Memory kind such as fact, preference, instruction, decision, procedure, summary, or reference."`
	Visibility  Visibility         `json:"visibility,omitempty"`
	Subjects    []Subject          `json:"subjects,omitempty" jsonschema:"description=Things this memory is about. This is separate from access_scope."`
	AccessScope coredata.Scope     `json:"access_scope" jsonschema:"description=Who or what may retrieve/use this memory. This is not necessarily the subject."`
	Title       string             `json:"title,omitempty"`
	Content     string             `json:"content" jsonschema:"description=Concise natural-language memory content."`
	Data        map[string]any     `json:"data,omitempty"`
	Tags        []string           `json:"tags,omitempty"`
	SourceRefs  []SourceRef        `json:"source_refs,omitempty"`
	BlobRefs    []coredata.BlobRef `json:"blob_refs,omitempty"`
	Supersedes  []ID               `json:"supersedes,omitempty"`
	Sensitivity policy.Sensitivity `json:"sensitivity,omitempty"`
	ExpiresAt   time.Time          `json:"expires_at,omitempty"`
}

type MemorizeResult struct {
	Memory Memory `json:"memory"`
}

type RetrieveRequest struct {
	AccessScope      coredata.Scope     `json:"access_scope,omitempty"`
	Subjects         []Subject          `json:"subjects,omitempty"`
	IDs              []ID               `json:"ids,omitempty"`
	Kinds            []Kind             `json:"kinds,omitempty"`
	Tags             []string           `json:"tags,omitempty"`
	Text             string             `json:"text,omitempty"`
	Limit            int                `json:"limit,omitempty"`
	Cursor           string             `json:"cursor,omitempty"`
	IncludeArchived  bool               `json:"include_archived,omitempty"`
	IncludeForgotten bool               `json:"include_forgotten,omitempty"`
	MinSensitivity   policy.Sensitivity `json:"min_sensitivity,omitempty"`
}

type RetrieveResult struct {
	Memories   []Memory `json:"memories,omitempty"`
	NextCursor string   `json:"next_cursor,omitempty"`
	Complete   bool     `json:"complete"`
}

type ForgetMode string

const (
	ForgetModeForget  ForgetMode = "forget"
	ForgetModeArchive ForgetMode = "archive"
	ForgetModeExpire  ForgetMode = "expire"
)

type ForgetRequest struct {
	AccessScope coredata.Scope `json:"access_scope"`
	IDs         []ID           `json:"ids,omitempty"`
	Subjects    []Subject      `json:"subjects,omitempty"`
	Query       string         `json:"query,omitempty"`
	Mode        ForgetMode     `json:"mode,omitempty"`
	Reason      string         `json:"reason,omitempty"`
}

type ForgetResult struct {
	Affected []ID       `json:"affected,omitempty"`
	Status   Status     `json:"status,omitempty"`
	Mode     ForgetMode `json:"mode,omitempty"`
}

type OrganizeAction string

const (
	OrganizeRetag     OrganizeAction = "retag"
	OrganizeMerge     OrganizeAction = "merge"
	OrganizeSupersede OrganizeAction = "supersede"
	OrganizeArchive   OrganizeAction = "archive"
	OrganizeSummarize OrganizeAction = "summarize"
)

type OrganizeRequest struct {
	AccessScope coredata.Scope `json:"access_scope"`
	IDs         []ID           `json:"ids"`
	Action      OrganizeAction `json:"action"`
	Title       string         `json:"title,omitempty"`
	Content     string         `json:"content,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Subjects    []Subject      `json:"subjects,omitempty"`
	Reason      string         `json:"reason,omitempty"`
}

type OrganizeResult struct {
	Memories []Memory `json:"memories,omitempty"`
}

const (
	EventMemorizedName event.Name = "memory.memorized"
	EventForgottenName event.Name = "memory.forgotten"
	EventOrganizedName event.Name = "memory.organized"
)

type Memorized struct {
	Memory Memory `json:"memory"`
}

func (Memorized) EventName() event.Name { return EventMemorizedName }

type Forgotten struct {
	IDs    []ID       `json:"ids"`
	Status Status     `json:"status"`
	Mode   ForgetMode `json:"mode,omitempty"`
	Reason string     `json:"reason,omitempty"`
}

func (Forgotten) EventName() event.Name { return EventForgottenName }

type Organized struct {
	Memories []Memory       `json:"memories,omitempty"`
	Action   OrganizeAction `json:"action"`
	Reason   string         `json:"reason,omitempty"`
}

func (Organized) EventName() event.Name { return EventOrganizedName }
