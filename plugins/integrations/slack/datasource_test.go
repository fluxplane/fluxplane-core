package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/slack-go/slack"
)

func TestListChannelsSkipsConversationTypesMissingScope(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations.list" {
			t.Fatalf("path = %q, want /conversations.list", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		typ := r.Form.Get("types")
		seen = append(seen, typ)
		w.Header().Set("Content-Type", "application/json")
		switch typ {
		case "public_channel":
			_, _ = w.Write([]byte(`{"ok":true,"channels":[{"id":"C1","name":"general","is_channel":true}]}`))
		case "private_channel":
			_, _ = w.Write([]byte(`{"ok":true,"channels":[{"id":"G1","name":"private","is_group":true}]}`))
		case "im":
			_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope","needed":"im:read","provided":"channels:read,groups:read"}`))
		case "mpim":
			_, _ = w.Write([]byte(`{"ok":false,"error":"missing_scope","needed":"mpim:read","provided":"channels:read,groups:read"}`))
		default:
			t.Fatalf("types = %q", typ)
		}
	}))
	defer server.Close()

	channels, err := listChannels(context.Background(), slack.New("xoxb-test", slack.OptionAPIURL(server.URL+"/")), 20)
	if err != nil {
		t.Fatalf("listChannels: %v", err)
	}
	if got := channelIDs(channels); !reflect.DeepEqual(got, []string{"C1", "G1"}) {
		t.Fatalf("channels = %#v, want C1/G1", got)
	}
	if !reflect.DeepEqual(seen, slackConversationTypes) {
		t.Fatalf("seen types = %#v, want %#v", seen, slackConversationTypes)
	}
}

func channelIDs(channels []slack.Channel) []string {
	out := make([]string, 0, len(channels))
	for _, channel := range channels {
		out = append(out, channel.ID)
	}
	return out
}
