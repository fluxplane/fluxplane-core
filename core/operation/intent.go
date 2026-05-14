package operation

// IntentProvider is implemented by operations that can describe their expected
// external effects from the concrete input before execution.
type IntentProvider interface {
	Intent(Context, Value) (IntentSet, error)
}

// IntentSet describes the external effects an operation intends to perform.
type IntentSet struct {
	Operations []IntentOperation
}

// Empty reports whether the set contains no operation intents.
func (s IntentSet) Empty() bool {
	return len(s.Operations) == 0
}

// IntentOperation describes one intended external behavior.
type IntentOperation struct {
	Behavior  IntentBehavior
	Target    IntentTarget
	Role      IntentRole
	Certainty IntentCertainty
}

// IntentBehavior names a typed class of intended behavior.
type IntentBehavior string

const (
	IntentCommandExecution  IntentBehavior = "command_execution"
	IntentFilesystemRead    IntentBehavior = "filesystem_read"
	IntentFilesystemWrite   IntentBehavior = "filesystem_write"
	IntentPersistenceModify IntentBehavior = "persistence_modify"
	IntentNetworkFetch      IntentBehavior = "network_fetch"
	IntentBrowserAccess     IntentBehavior = "browser_access"
)

// IntentRole describes how a target participates in an intent.
type IntentRole string

const (
	IntentRoleReadTarget      IntentRole = "read_target"
	IntentRoleWriteTarget     IntentRole = "write_target"
	IntentRoleProcessCommand  IntentRole = "process_command"
	IntentRoleNetworkTarget   IntentRole = "network_target"
	IntentRoleBrowserTarget   IntentRole = "browser_target"
	IntentRoleWorkspaceTarget IntentRole = "workspace_target"
)

// IntentCertainty records whether the target is definitely or conditionally
// touched by the operation.
type IntentCertainty string

const (
	IntentCertain   IntentCertainty = "certain"
	IntentPotential IntentCertainty = "potential"
)

// Path is a typed filesystem path used by operation intent.
type Path string

// URL is a typed URL used by operation intent.
type URL string

// Command is a typed process command used by operation intent.
type Command string

// Argument is a typed process argument used by operation intent.
type Argument string

// Workdir is a typed process working directory used by operation intent.
type Workdir string

// SessionID is a typed browser or managed-session id used by operation intent.
type SessionID string

// IntentTarget marks typed intent target variants.
type IntentTarget interface {
	intentTarget()
}

// PathTarget identifies a filesystem path target.
type PathTarget struct {
	Path Path
}

func (PathTarget) intentTarget() {}

// URLTarget identifies a URL target.
type URLTarget struct {
	URL URL
}

func (URLTarget) intentTarget() {}

// ProcessTarget identifies a direct process execution target.
type ProcessTarget struct {
	Command Command
	Args    []Argument
	Workdir Workdir
}

func (ProcessTarget) intentTarget() {}

// BrowserTarget identifies browser access tied to a URL or existing session.
type BrowserTarget struct {
	URL       URL
	SessionID SessionID
}

func (BrowserTarget) intentTarget() {}

// IntentFor returns the operation-provided intent, when available.
func IntentFor(ctx Context, op Operation, input Value) (IntentSet, bool, error) {
	provider, ok := op.(IntentProvider)
	if !ok {
		return IntentSet{}, false, nil
	}
	intents, err := provider.Intent(ctx, input)
	return intents, true, err
}
