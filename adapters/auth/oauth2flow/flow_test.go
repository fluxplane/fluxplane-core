package oauth2flow

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestCallbackHandlerDoesNotBlockOnSecondCallback(t *testing.T) {
	out := make(chan callbackResult, 1)
	handler := callbackHandler("expected-state", out)

	const callbacks = 5
	var wg sync.WaitGroup
	wg.Add(callbacks)
	done := make(chan struct{})
	for i := 0; i < callbacks; i++ {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "/callback?state=expected-state&code=abc", nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
		}()
	}
	go func() {
		wg.Wait()
		close(done)
	}()

	// First handler delivers, the rest are dropped by sync.Once. All five
	// must finish — if any blocked on the channel send, wg.Wait would never
	// fire and this test would hang.
	<-done

	select {
	case got := <-out:
		if got.code != "abc" {
			t.Fatalf("delivered result = %+v, want code=abc", got)
		}
	default:
		t.Fatal("no result delivered to channel")
	}

	// And no further results are pending — only the first should have been
	// delivered.
	select {
	case extra := <-out:
		t.Fatalf("unexpected second delivery %+v, sync.Once should have suppressed it", extra)
	default:
	}
}
