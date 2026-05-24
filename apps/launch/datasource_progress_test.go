package launch

import (
	"strings"
	"testing"
	"time"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/orchestration/datasourceindex"
)

func TestDatasourceIndexPageTextIncludesProgressAndGitLabMembershipCursor(t *testing.T) {
	event := datasourceindex.ProgressEvent{
		Datasource:    "gitlab",
		Entity:        "gitlab.user_membership",
		Phase:         datasourceindex.PhaseAll,
		Cursor:        "project:4:82:1",
		NextCursor:    "project:4:83:1",
		Page:          183,
		PageDocuments: 20,
		Documents:     3660,
		Indexed:       3660,
		Failed:        1,
		FirstID:       "12:project:991",
		LastID:        "44:project:991",
		Elapsed:       2*time.Minute + 14*time.Second,
		Rate:          27.3,
	}
	text := datasourceIndexPageText(event)
	for _, want := range []string{
		"page=183",
		"page_documents=20",
		"documents=3660",
		"indexed=3660",
		"failed=1",
		"cursor=project:4:82:1",
		"next_cursor=project:4:83:1",
		"membership_phase=project",
		"source_page=4",
		"source_index=82",
		"member_page=1",
		"first_id=12:project:991",
		"last_id=44:project:991",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("page text %q missing %q", text, want)
		}
	}
}

func TestDatasourceIndexPageLogArgsIncludesGitLabMembershipCursor(t *testing.T) {
	event := datasourceindex.ProgressEvent{
		Datasource: "gitlab",
		Entity:     coredatasource.EntityType("gitlab.user_membership"),
		Phase:      datasourceindex.PhaseAll,
		Cursor:     "group:2:3:4",
		Page:       5,
		Documents:  100,
	}
	args := datasourceIndexPageLogArgs(event)
	assertLogArg(t, args, "membership_phase", "group")
	assertLogArg(t, args, "source_page", 2)
	assertLogArg(t, args, "source_index", 3)
	assertLogArg(t, args, "member_page", 4)
	assertLogArg(t, args, "documents", 100)
}

func assertLogArg(t *testing.T, args []any, key string, want any) {
	t.Helper()
	for i := 0; i+1 < len(args); i += 2 {
		if args[i] == key {
			if args[i+1] != want {
				t.Fatalf("arg %s = %#v, want %#v", key, args[i+1], want)
			}
			return
		}
	}
	t.Fatalf("arg %s missing in %#v", key, args)
}
