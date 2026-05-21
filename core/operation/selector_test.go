package operation

import "testing"

func TestRefMatchesExactAndWildcardSelectors(t *testing.T) {
	tests := []struct {
		name      string
		selector  Ref
		candidate Ref
		want      bool
	}{
		{name: "exact", selector: Ref{Name: "gitlab_mr"}, candidate: Ref{Name: "gitlab_mr"}, want: true},
		{name: "prefix wildcard", selector: Ref{Name: "gitlab_*"}, candidate: Ref{Name: "gitlab_mr"}, want: true},
		{name: "prefix wildcard mismatch", selector: Ref{Name: "gitlab_*"}, candidate: Ref{Name: "jira_issue_search"}, want: false},
		{name: "version match", selector: Ref{Name: "gitlab_*", Version: "v1"}, candidate: Ref{Name: "gitlab_mr", Version: "v1"}, want: true},
		{name: "version mismatch", selector: Ref{Name: "gitlab_*", Version: "v1"}, candidate: Ref{Name: "gitlab_mr", Version: "v2"}, want: false},
		{name: "empty selector", selector: Ref{}, candidate: Ref{Name: "gitlab_mr"}, want: false},
		{name: "empty candidate", selector: Ref{Name: "gitlab_*"}, candidate: Ref{}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.selector.Matches(tt.candidate); got != tt.want {
				t.Fatalf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}
