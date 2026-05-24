package slack

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	coredatasource "github.com/fluxplane/fluxplane-core/core/datasource"
	"github.com/fluxplane/fluxplane-core/core/resource"
	runtimesecret "github.com/fluxplane/fluxplane-core/runtime/secret"
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

	channels, err := listChannels(context.Background(), slack.New("slack-bot-token", slack.OptionAPIURL(server.URL+"/")), 20)
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

func TestMessageSearchRequiresUserTokenForNativeSearch(t *testing.T) {
	store := runtimesecret.NewFileStore(t.TempDir())
	ref := resource.PluginRef{Name: Name, Instance: "main"}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{Ref: BotTokenSecretRef(ref), Value: "slack-bot-token"}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	plugin := New(nil, store)
	plugin.ref = ref
	accessor := slackAccessor{spec: coredatasource.Spec{Name: "slack"}, plugin: plugin, entities: entitySpecs()}

	_, err := accessor.Search(context.Background(), coredatasource.SearchRequest{Entity: MessageEntity, Query: "hello"})
	if err == nil || !strings.Contains(err.Error(), "user token") {
		t.Fatalf("Search error = %v, want user token requirement", err)
	}
	if !accessor.ProviderSearchFallback(MessageEntity, err) {
		t.Fatalf("ProviderSearchFallback = false, want true")
	}
}

func TestMessageSearchUsesUserTokenForNativeSearch(t *testing.T) {
	var token string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search.messages" {
			t.Fatalf("path = %q, want /search.messages", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		token = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			token = r.Form.Get("token")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"messages":{"total":1,"matches":[{"type":"message","user":"U1","username":"timo","text":"hello native","ts":"1779653878.825989","permalink":"https://example.slack.com/archives/C1/p1779653878825989","channel":{"id":"C1","name":"dev-team"}}]}}`))
	}))
	defer server.Close()

	store := runtimesecret.NewFileStore(t.TempDir())
	ref := resource.PluginRef{Name: Name, Instance: "main"}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{Ref: BotTokenSecretRef(ref), Value: "slack-bot-token"}); err != nil {
		t.Fatalf("SaveSecret bot: %v", err)
	}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{Ref: UserTokenSecretRef(ref), Value: "slack-user-token"}); err != nil {
		t.Fatalf("SaveSecret user: %v", err)
	}
	plugin := New(nil, store)
	plugin.ref = ref
	plugin.clientFactory = func(token, appToken string) *slack.Client {
		return slack.New(token, slack.OptionAPIURL(server.URL+"/"))
	}
	accessor := slackAccessor{spec: coredatasource.Spec{Name: "slack"}, plugin: plugin, entities: entitySpecs()}

	result, err := accessor.Search(context.Background(), coredatasource.SearchRequest{Entity: MessageEntity, Query: "hello"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if token != "slack-user-token" {
		t.Fatalf("token = %q, want user token", token)
	}
	if len(result.Records) != 1 || result.Records[0].Content != "hello native" {
		t.Fatalf("records = %#v, want native message", result.Records)
	}
}

func TestMessageCorpusIndexesConfiguredChannels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth.test":
			_, _ = w.Write([]byte(`{"ok":true,"url":"https://example.slack.com/","team":"Example","team_id":"T1","user":"bot","user_id":"Ubot"}`))
		case "/conversations.list":
			_, _ = w.Write([]byte(`{"ok":true,"channels":[{"id":"C1","name":"dev-team","is_channel":true},{"id":"C2","name":"general-ai","is_channel":true}]}`))
		case "/conversations.history":
			if r.Form.Get("channel") != "C1" {
				t.Fatalf("history channel = %q, want C1", r.Form.Get("channel"))
			}
			_, _ = w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U1","text":"indexed root","ts":"1779653878.825989","reply_count":1}]}`))
		case "/conversations.replies":
			if r.Form.Get("channel") != "C1" || r.Form.Get("ts") != "1779653878.825989" {
				t.Fatalf("replies channel=%q ts=%q", r.Form.Get("channel"), r.Form.Get("ts"))
			}
			_, _ = w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U1","text":"indexed root","ts":"1779653878.825989"},{"type":"message","user":"U2","text":"indexed reply","ts":"1779653880.000000","thread_ts":"1779653878.825989"}]}`))
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	store := runtimesecret.NewFileStore(t.TempDir())
	ref := resource.PluginRef{Name: Name, Instance: "main"}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{Ref: BotTokenSecretRef(ref), Value: "slack-bot-token"}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	includeThreads := true
	plugin := New(nil, store)
	plugin.ref = ref
	plugin.cfg.Search = SearchConfig{Channels: []string{"dev-team"}, HistoryWindow: "90d", IncludeThreads: &includeThreads}
	plugin.clientFactory = func(token, appToken string) *slack.Client {
		if token != "slack-bot-token" {
			t.Fatalf("token = %q, want bot token", token)
		}
		return slack.New(token, slack.OptionAPIURL(server.URL+"/"))
	}
	accessor := slackAccessor{spec: coredatasource.Spec{Name: "slack"}, plugin: plugin, entities: entitySpecs(), search: plugin.cfg.Search}

	page, err := accessor.Corpus(context.Background(), coredatasource.CorpusRequest{Entity: MessageEntity, Limit: 20})
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	if !page.Complete || page.NextCursor != "" {
		t.Fatalf("page complete=%v next=%q, want complete final page", page.Complete, page.NextCursor)
	}
	if len(page.Documents) != 2 {
		t.Fatalf("documents = %d, want root and reply", len(page.Documents))
	}
	if page.Documents[0].Body != "indexed root" || page.Documents[1].Body != "indexed reply" {
		t.Fatalf("documents = %#v, want root and reply bodies", page.Documents)
	}
	if page.Documents[0].URL != "https://example.slack.com/archives/C1/p1779653878825989" || page.Documents[0].Metadata["permalink"] != page.Documents[0].URL {
		t.Fatalf("root URL=%q metadata=%#v, want canonical Slack permalink", page.Documents[0].URL, page.Documents[0].Metadata)
	}
	if page.Documents[1].URL != "https://example.slack.com/archives/C1/p1779653880000000" || page.Documents[1].Metadata["permalink"] != page.Documents[1].URL {
		t.Fatalf("reply URL=%q metadata=%#v, want canonical Slack permalink", page.Documents[1].URL, page.Documents[1].Metadata)
	}
}

func TestMessageCorpusSkipsChannelsBotCannotRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/auth.test":
			_, _ = w.Write([]byte(`{"ok":true,"url":"https://example.slack.com/","team":"Example","team_id":"T1","user":"bot","user_id":"Ubot"}`))
		case "/conversations.list":
			_, _ = w.Write([]byte(`{"ok":true,"channels":[{"id":"C1","name":"not-joined","is_channel":true},{"id":"C2","name":"dev-team","is_channel":true}]}`))
		case "/conversations.history":
			switch r.Form.Get("channel") {
			case "C1":
				_, _ = w.Write([]byte(`{"ok":false,"error":"not_in_channel"}`))
			case "C2":
				_, _ = w.Write([]byte(`{"ok":true,"messages":[{"type":"message","user":"U1","text":"indexed accessible","ts":"1779653878.825989"}]}`))
			default:
				t.Fatalf("history channel = %q", r.Form.Get("channel"))
			}
		default:
			t.Fatalf("path = %q", r.URL.Path)
		}
	}))
	defer server.Close()

	store := runtimesecret.NewFileStore(t.TempDir())
	ref := resource.PluginRef{Name: Name, Instance: "main"}
	if err := store.SaveSecret(context.Background(), runtimesecret.StoredSecret{Ref: BotTokenSecretRef(ref), Value: "slack-bot-token"}); err != nil {
		t.Fatalf("SaveSecret: %v", err)
	}
	includeThreads := false
	plugin := New(nil, store)
	plugin.ref = ref
	plugin.cfg.Search = SearchConfig{Channels: []string{"not-joined", "dev-team"}, IncludeThreads: &includeThreads}
	plugin.clientFactory = func(token, appToken string) *slack.Client {
		return slack.New(token, slack.OptionAPIURL(server.URL+"/"))
	}
	accessor := slackAccessor{spec: coredatasource.Spec{Name: "slack"}, plugin: plugin, entities: entitySpecs(), search: plugin.cfg.Search}

	page, err := accessor.Corpus(context.Background(), coredatasource.CorpusRequest{Entity: MessageEntity, Limit: 20})
	if err != nil {
		t.Fatalf("Corpus: %v", err)
	}
	if len(page.Documents) != 1 || page.Documents[0].Body != "indexed accessible" {
		t.Fatalf("documents = %#v, want accessible channel document", page.Documents)
	}
}

func TestSlackPermalinkBuildsCanonicalMessageURL(t *testing.T) {
	got := slackPermalink("https://example.slack.com/", "C1", "1779653880.42")
	want := "https://example.slack.com/archives/C1/p1779653880420000"
	if got != want {
		t.Fatalf("slackPermalink = %q, want %q", got, want)
	}
}

func channelIDs(channels []slack.Channel) []string {
	out := make([]string, 0, len(channels))
	for _, channel := range channels {
		out = append(out, channel.ID)
	}
	return out
}
