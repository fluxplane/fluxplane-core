package operation

import fpoperation "github.com/fluxplane/fluxplane-operation"

type CallID = fpoperation.CallID

var WithCallID = fpoperation.WithCallID
var CallIDFromContext = fpoperation.CallIDFromContext

const (
	EventStartedName   = fpoperation.EventStartedName
	EventCompletedName = fpoperation.EventCompletedName
	EventFailedName    = fpoperation.EventFailedName
	EventRejectedName  = fpoperation.EventRejectedName
	EventCanceledName  = fpoperation.EventCanceledName
)

type OperationStarted = fpoperation.OperationStarted
type OperationCompleted = fpoperation.OperationCompleted
type OperationFailed = fpoperation.OperationFailed
type OperationRejected = fpoperation.OperationRejected
type OperationCanceled = fpoperation.OperationCanceled
type ValueRef = fpoperation.ValueRef

type IntentProvider = fpoperation.IntentProvider
type IntentSet = fpoperation.IntentSet
type IntentOperation = fpoperation.IntentOperation
type IntentBehavior = fpoperation.IntentBehavior

const (
	IntentCommandExecution  = fpoperation.IntentCommandExecution
	IntentFilesystemRead    = fpoperation.IntentFilesystemRead
	IntentFilesystemWrite   = fpoperation.IntentFilesystemWrite
	IntentPersistenceModify = fpoperation.IntentPersistenceModify
	IntentNetworkFetch      = fpoperation.IntentNetworkFetch
	IntentNetworkWrite      = fpoperation.IntentNetworkWrite
	IntentBrowserAccess     = fpoperation.IntentBrowserAccess
)

type IntentRole = fpoperation.IntentRole

const (
	IntentRoleReadTarget      = fpoperation.IntentRoleReadTarget
	IntentRoleWriteTarget     = fpoperation.IntentRoleWriteTarget
	IntentRoleProcessCommand  = fpoperation.IntentRoleProcessCommand
	IntentRoleNetworkTarget   = fpoperation.IntentRoleNetworkTarget
	IntentRoleBrowserTarget   = fpoperation.IntentRoleBrowserTarget
	IntentRoleWorkspaceTarget = fpoperation.IntentRoleWorkspaceTarget
)

type IntentCertainty = fpoperation.IntentCertainty

const (
	IntentCertain   = fpoperation.IntentCertain
	IntentPotential = fpoperation.IntentPotential
)

type Path = fpoperation.Path
type URL = fpoperation.URL
type Command = fpoperation.Command
type Argument = fpoperation.Argument
type Workdir = fpoperation.Workdir
type SessionID = fpoperation.SessionID
type IntentTarget = fpoperation.IntentTarget
type PathTarget = fpoperation.PathTarget
type URLTarget = fpoperation.URLTarget
type ProcessTarget = fpoperation.ProcessTarget
type BrowserTarget = fpoperation.BrowserTarget

var IntentFor = fpoperation.IntentFor

type Context = fpoperation.Context
type Operation = fpoperation.Operation
type Handler = fpoperation.Handler
type HandlerOperation = fpoperation.HandlerOperation

var New = fpoperation.New
var NewContext = fpoperation.NewContext

type Name = fpoperation.Name
type Version = fpoperation.Version
type Ref = fpoperation.Ref

var HasSelectorMeta = fpoperation.HasSelectorMeta

type Registry = fpoperation.Registry

var NewRegistry = fpoperation.NewRegistry

type Status = fpoperation.Status

const (
	StatusOK       = fpoperation.StatusOK
	StatusFailed   = fpoperation.StatusFailed
	StatusRejected = fpoperation.StatusRejected
	StatusCanceled = fpoperation.StatusCanceled
)

type Error = fpoperation.Error
type Result = fpoperation.Result
type ModelRenderable = fpoperation.ModelRenderable
type Rendered = fpoperation.Rendered

var OK = fpoperation.OK
var Failed = fpoperation.Failed
var Rejected = fpoperation.Rejected
var Canceled = fpoperation.Canceled

type Determinism = fpoperation.Determinism

const (
	DeterminismUnknown          = fpoperation.DeterminismUnknown
	DeterminismDeterministic    = fpoperation.DeterminismDeterministic
	DeterminismNonDeterministic = fpoperation.DeterminismNonDeterministic
)

var Determinisms = fpoperation.Determinisms

type Idempotency = fpoperation.Idempotency

const (
	IdempotencyUnknown       = fpoperation.IdempotencyUnknown
	IdempotencyIdempotent    = fpoperation.IdempotencyIdempotent
	IdempotencyNonIdempotent = fpoperation.IdempotencyNonIdempotent
	IdempotencyConditional   = fpoperation.IdempotencyConditional
	IdempotencyUnknownText   = fpoperation.IdempotencyUnknownText
)

var Idempotencies = fpoperation.Idempotencies

type RiskLevel = fpoperation.RiskLevel

const (
	RiskUnknown     = fpoperation.RiskUnknown
	RiskLow         = fpoperation.RiskLow
	RiskMedium      = fpoperation.RiskMedium
	RiskHigh        = fpoperation.RiskHigh
	RiskCritical    = fpoperation.RiskCritical
	RiskDestructive = fpoperation.RiskDestructive
)

var RiskLevels = fpoperation.RiskLevels
var ParseRiskLevel = fpoperation.ParseRiskLevel

type Effect = fpoperation.Effect

const (
	EffectNone          = fpoperation.EffectNone
	EffectReadExternal  = fpoperation.EffectReadExternal
	EffectWriteExternal = fpoperation.EffectWriteExternal
	EffectFilesystem    = fpoperation.EffectFilesystem
	EffectNetwork       = fpoperation.EffectNetwork
	EffectProcess       = fpoperation.EffectProcess
	EffectCreate        = fpoperation.EffectCreate
	EffectUpdate        = fpoperation.EffectUpdate
	EffectDelete        = fpoperation.EffectDelete
	EffectDestructive   = fpoperation.EffectDestructive
	EffectIrreversible  = fpoperation.EffectIrreversible
	EffectSensitiveData = fpoperation.EffectSensitiveData
)

var Effects = fpoperation.Effects

type EffectSet = fpoperation.EffectSet
type Semantics = fpoperation.Semantics
type Set = fpoperation.Set
type Value = fpoperation.Value
type Schema = fpoperation.Schema
type Type = fpoperation.Type
type Example = fpoperation.Example
type Spec = fpoperation.Spec
