package thread

import corethread "github.com/fluxplane/engine/core/thread"

func listFromReadIndex(index *ThreadIndex, params corethread.ListParams) corethread.Page {
	entries := index.List(params)
	page := corethread.Page{Threads: make([]corethread.Snapshot, 0, len(entries))}
	for _, entry := range entries {
		page.Threads = append(page.Threads, corethread.Snapshot{
			ID:        entry.ID,
			BranchID:  entry.BranchID,
			Metadata:  cloneStringMap(entry.Metadata),
			Archived:  entry.Archived,
			CreatedAt: entry.CreatedAt,
			UpdatedAt: entry.UpdatedAt,
		})
	}
	return page
}
