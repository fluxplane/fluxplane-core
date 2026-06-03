package workspace

import fpworkspace "github.com/fluxplane/fluxplane-workspace"

type ID = fpworkspace.ID

type RootKind = fpworkspace.RootKind

const (
	RootLocal       = fpworkspace.RootLocal
	RootGitWorktree = fpworkspace.RootGitWorktree
	RootVirtual     = fpworkspace.RootVirtual
	RootRemote      = fpworkspace.RootRemote
)

type OriginKind = fpworkspace.OriginKind

const (
	OriginConfigured = fpworkspace.OriginConfigured
	OriginLocal      = fpworkspace.OriginLocal
	OriginGitHub     = fpworkspace.OriginGitHub
	OriginGitLab     = fpworkspace.OriginGitLab
	OriginGit        = fpworkspace.OriginGit
)

type Durability = fpworkspace.Durability

const (
	DurabilityEphemeral = fpworkspace.DurabilityEphemeral
	DurabilityDurable   = fpworkspace.DurabilityDurable
)

type Workspace = fpworkspace.Workspace
type Root = fpworkspace.Root
type Origin = fpworkspace.Origin
type Alias = fpworkspace.Alias
type Selection = fpworkspace.Selection
