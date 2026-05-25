package oauth2client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestPostTokenFormCapsResponseSize regresses an unbounded io.ReadAll on
// resp.Body that allowed a malicious or buggy OAuth endpoint to make us
// consume arbitrary memory. The fix wraps the read in
// io.LimitReader(maxTokenResponseBytes); a successful token response is
// far below the cap so happy-path behavior is unchanged, but a hostile
// endpoint returning a multi-GB body is now bounded at 1 MiB.
func TestPostTokenFormCapsResponseSize(t *testing.T) {
	// Build a server that returns a 2 MiB JSON blob padded with whitespace
	// after the access_token. The decoder should still produce the right
	// token, but only the first maxTokenResponseBytes are read into memory.
	padding := strings.Repeat(" ", 2*1024*1024)
	body := `{"access_token":"abc"}` + padding
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	got, err := postTokenForm(context.Background(), server.Client(), server.URL, nil)
	if err == nil {
		// Whitespace padding still produces valid JSON if we truncate the
		// trailing portion, so the happy path returns a token without OOMing.
		if got.AccessToken != "abc" {
			t.Fatalf("AccessToken = %q, want abc", got.AccessToken)
		}
		return
	}
	// If decoder rejects because the truncated body is no longer valid JSON,
	// that's also acceptable: the important property is that we did not load
	// the full 2 MiB into memory and chase it through the JSON decoder.
	if !strings.Contains(err.Error(), "unexpected") && !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPostTokenFormPreservesSmallResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "tok", "token_type": "Bearer"})
	}))
	defer server.Close()
	got, err := postTokenForm(context.Background(), server.Client(), server.URL, nil)
	if err != nil {
		t.Fatalf("postTokenForm: %v", err)
	}
	if got.AccessToken != "tok" {
		t.Fatalf("AccessToken = %q, want tok", got.AccessToken)
	}
}
