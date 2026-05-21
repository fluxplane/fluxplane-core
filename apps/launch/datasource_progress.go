package launch

import (
	"fmt"
	"strconv"
	"strings"

	coredatasource "github.com/fluxplane/engine/core/datasource"
	"github.com/fluxplane/engine/orchestration/datasourceindex"
)

func datasourceIndexPageLogArgs(event datasourceindex.ProgressEvent) []any {
	args := []any{
		"datasource", event.Datasource,
		"entity", event.Entity,
		"phase", event.Phase,
		"page", event.Page,
		"page_documents", event.PageDocuments,
		"documents", event.Documents,
		"indexed", event.Indexed,
		"queued", event.Queued,
		"skipped", event.Skipped,
		"deleted", event.Deleted,
		"failed", event.Failed,
		"tombstones", event.Tombstones,
		"complete", event.Complete,
		"elapsed_ms", event.Elapsed.Milliseconds(),
		"rate_docs_per_sec", event.Rate,
	}
	if strings.TrimSpace(event.Cursor) != "" {
		args = append(args, "cursor", event.Cursor)
	}
	if strings.TrimSpace(event.NextCursor) != "" {
		args = append(args, "next_cursor", event.NextCursor)
	}
	if strings.TrimSpace(event.FirstID) != "" {
		args = append(args, "first_id", event.FirstID)
	}
	if strings.TrimSpace(event.LastID) != "" {
		args = append(args, "last_id", event.LastID)
	}
	args = append(args, gitLabMembershipCursorLogArgs(event)...)
	return args
}

func datasourceIndexPageText(event datasourceindex.ProgressEvent) string {
	base := fmt.Sprintf(
		"index %s/%s phase=%s page=%d page_documents=%d documents=%d indexed=%d queued=%d skipped=%d deleted=%d failed=%d tombstones=%d complete=%t elapsed_ms=%d rate_docs_per_sec=%.1f",
		event.Datasource,
		event.Entity,
		event.Phase,
		event.Page,
		event.PageDocuments,
		event.Documents,
		event.Indexed,
		event.Queued,
		event.Skipped,
		event.Deleted,
		event.Failed,
		event.Tombstones,
		event.Complete,
		event.Elapsed.Milliseconds(),
		event.Rate,
	)
	var fields []string
	if strings.TrimSpace(event.Cursor) != "" {
		fields = append(fields, "cursor="+event.Cursor)
	}
	if strings.TrimSpace(event.NextCursor) != "" {
		fields = append(fields, "next_cursor="+event.NextCursor)
	}
	if strings.TrimSpace(event.FirstID) != "" {
		fields = append(fields, "first_id="+event.FirstID)
	}
	if strings.TrimSpace(event.LastID) != "" {
		fields = append(fields, "last_id="+event.LastID)
	}
	cursorArgs := gitLabMembershipCursorLogArgs(event)
	for i := 0; i+1 < len(cursorArgs); i += 2 {
		fields = append(fields, fmt.Sprintf("%s=%v", cursorArgs[i], cursorArgs[i+1]))
	}
	if len(fields) == 0 {
		return base
	}
	return base + " " + strings.Join(fields, " ")
}

func gitLabMembershipCursorLogArgs(event datasourceindex.ProgressEvent) []any {
	if event.Entity != coredatasource.EntityType("gitlab.user_membership") {
		return nil
	}
	cursor := strings.TrimSpace(event.Cursor)
	if cursor == "" {
		cursor = strings.TrimSpace(event.NextCursor)
	}
	parts := strings.Split(cursor, ":")
	if len(parts) != 4 {
		return nil
	}
	sourcePage, err1 := strconv.Atoi(parts[1])
	sourceIndex, err2 := strconv.Atoi(parts[2])
	memberPage, err3 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return nil
	}
	return []any{
		"membership_phase", parts[0],
		"source_page", sourcePage,
		"source_index", sourceIndex,
		"member_page", memberPage,
	}
}

func indexedDatasourceJobLabels(registry *coredatasource.Registry) []string {
	if registry == nil {
		return nil
	}
	var jobs []string
	for _, accessor := range registry.All() {
		spec := accessor.Spec()
		if !spec.Index.Enabled {
			continue
		}
		for _, entity := range accessor.Entities() {
			if entity.Supports(coredatasource.EntityCapabilityIndex) || entity.Supports(coredatasource.EntityCapabilitySemanticSearch) {
				jobs = append(jobs, string(spec.Name)+"/"+string(entity.Type))
			}
		}
	}
	return jobs
}
