package thread

// ID identifies a conversation thread.
type ID string

// BranchID identifies one branch of a thread.
type BranchID string

// NodeID identifies a node within a thread branch.
type NodeID string

// MainBranch is the default branch name.
const MainBranch BranchID = "main"

// Ref identifies a thread branch.
type Ref struct {
	ID       ID       `json:"id"`
	BranchID BranchID `json:"branch_id,omitempty"`
}
