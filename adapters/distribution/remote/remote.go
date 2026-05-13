package remote

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fluxplane/agentruntime/adapters/appconfig"
	distserve "github.com/fluxplane/agentruntime/adapters/distribution/serve"
	"github.com/fluxplane/agentruntime/adapters/httpssechannel"
	"github.com/fluxplane/agentruntime/adapters/terminalui"
	"github.com/fluxplane/agentruntime/core/channel"
	coreevent "github.com/fluxplane/agentruntime/core/event"
	coresession "github.com/fluxplane/agentruntime/core/session"
	"github.com/fluxplane/agentruntime/core/usage"
	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
	"github.com/spf13/cobra"
)

type Options struct {
	AppDir              string
	URL                 string
	Socket              string
	Local               bool
	Session             string
	SessionExplicit     bool
	Conversation        string
	Input               string
	Debug               bool
	Usage               bool
	DefaultSession      string
	DefaultConversation string
	DefaultSocket       string
	Events              *coreevent.Registry
	In                  io.Reader
	Out                 io.Writer
	Err                 io.Writer
}

type Target struct {
	BaseURL     string
	Socket      string
	BearerToken string
	Session     string
}

type CommandOptions struct {
	DefaultSession      string
	DefaultConversation string
	DefaultSocket       string
	Events              *coreevent.Registry
}

type commandState struct {
	appDir          string
	url             string
	socket          string
	local           bool
	session         string
	sessionExplicit bool
	conversation    string
	input           string
	debug           bool
	usage           bool
}

func NewCommand(opts CommandOptions) *cobra.Command {
	var state commandState
	state.session = opts.DefaultSession
	state.conversation = opts.DefaultConversation
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Connect to a running agentsdk daemon session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			state.sessionExplicit = cmd.Flags().Changed("session")
			return Run(cmd.Context(), Options{
				AppDir:              state.appDir,
				URL:                 state.url,
				Socket:              state.socket,
				Local:               state.local,
				Session:             state.session,
				SessionExplicit:     state.sessionExplicit,
				Conversation:        state.conversation,
				Input:               state.input,
				Debug:               state.debug,
				Usage:               state.usage,
				DefaultSession:      opts.DefaultSession,
				DefaultConversation: opts.DefaultConversation,
				DefaultSocket:       opts.DefaultSocket,
				Events:              opts.Events,
				In:                  cmd.InOrStdin(),
				Out:                 cmd.OutOrStdout(),
				Err:                 cmd.ErrOrStderr(),
			})
		},
	}
	cmd.Flags().StringVar(&state.appDir, "app", "", "app directory to read daemon listener config from")
	cmd.Flags().StringVar(&state.url, "url", "", "HTTP/SSE daemon listener URL")
	cmd.Flags().StringVar(&state.socket, "socket", "", "Unix socket path or socket name")
	cmd.Flags().BoolVar(&state.local, "local", false, "connect to the default local Unix socket")
	cmd.Flags().StringVar(&state.session, "session", opts.DefaultSession, "configured session name to open")
	cmd.Flags().StringVar(&state.conversation, "conversation", opts.DefaultConversation, "remote conversation id")
	cmd.Flags().StringVar(&state.input, "input", "", "send one input and exit instead of opening a REPL")
	cmd.Flags().BoolVar(&state.debug, "debug", false, "print run events as highlighted JSON markdown")
	cmd.Flags().BoolVar(&state.usage, "usage", false, "print usage events after each response")
	return cmd
}

func Run(ctx context.Context, opts Options) error {
	session, err := OpenSession(ctx, opts)
	if err != nil {
		return err
	}
	tracker := usage.NewTracker()
	if strings.TrimSpace(opts.Input) != "" {
		return terminalui.RunTurn(ctx, session, opts.Input, terminalOptions(opts), tracker)
	}
	errOut := writerOr(opts.Err, os.Stderr)
	stdout := writerOr(opts.Out, os.Stdout)
	_, _ = fmt.Fprintln(errOut, "agentsdk remote. Type /exit or /quit to stop.")
	scanner := bufio.NewScanner(readerOr(opts.In, os.Stdin))
	for {
		_, _ = fmt.Fprint(stdout, "remote> ")
		if !scanner.Scan() {
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		switch prompt {
		case "":
			continue
		case "/exit", "/quit":
			return nil
		}
		if err := terminalui.RunTurn(ctx, session, prompt, terminalOptions(opts), tracker); err != nil {
			_, _ = fmt.Fprintf(errOut, "error: %v\n", err)
		}
	}
	return scanner.Err()
}

func OpenSession(ctx context.Context, opts Options) (clientapi.SessionHandle, error) {
	target, err := ResolveTarget(ctx, opts)
	if err != nil {
		return nil, err
	}
	client, err := httpssechannel.NewClient(httpssechannel.ClientConfig{
		BaseURL:     target.BaseURL,
		UnixSocket:  target.Socket,
		BearerToken: target.BearerToken,
		Events:      opts.Events,
	})
	if err != nil {
		return nil, err
	}
	sessionName := firstNonEmptyString(target.Session, opts.Session, opts.DefaultSession)
	conversation := strings.TrimSpace(opts.Conversation)
	if conversation == "" {
		conversation = opts.DefaultConversation
	}
	return client.Open(ctx, clientapi.OpenRequest{
		Session:      coresession.Ref{Name: coresession.Name(sessionName)},
		Conversation: channel.ConversationRef{ID: conversation},
	})
}

func ResolveTarget(ctx context.Context, opts Options) (Target, error) {
	var modes []string
	if strings.TrimSpace(opts.AppDir) != "" {
		modes = append(modes, "--app")
	}
	if strings.TrimSpace(opts.URL) != "" {
		modes = append(modes, "--url")
	}
	if strings.TrimSpace(opts.Socket) != "" {
		modes = append(modes, "--socket")
	}
	if opts.Local {
		modes = append(modes, "--local")
	}
	if len(modes) == 0 {
		return Target{}, fmt.Errorf("remote: specify one target with --app, --url, --socket, or --local")
	}
	if len(modes) > 1 {
		return Target{}, fmt.Errorf("remote: target flags are mutually exclusive: %s", strings.Join(modes, ", "))
	}
	switch modes[0] {
	case "--app":
		return resolveAppTarget(ctx, opts)
	case "--url":
		return Target{BaseURL: strings.TrimRight(strings.TrimSpace(opts.URL), "/"), Session: opts.Session}, nil
	case "--socket":
		return Target{BaseURL: "http://unix", Socket: ResolveSocketPath(opts.Socket), Session: opts.Session}, nil
	case "--local":
		return Target{BaseURL: "http://unix", Socket: ResolveSocketPath(opts.DefaultSocket), Session: opts.Session}, nil
	default:
		return Target{}, fmt.Errorf("remote: unsupported target %s", modes[0])
	}
}

func resolveAppTarget(ctx context.Context, opts Options) (Target, error) {
	cfgFile, err := appconfig.LoadDirFile(ctx, opts.AppDir)
	if err != nil {
		return Target{}, err
	}
	if err := cfgFile.Validate(); err != nil {
		return Target{}, err
	}
	ch, sessionName, err := selectDirectChannel(cfgFile.Daemon.Channels, opts.Session, opts.SessionExplicit)
	if err != nil {
		return Target{}, err
	}
	listener, err := listenerByName(cfgFile.Daemon.Listeners, ch.Listener)
	if err != nil {
		return Target{}, err
	}
	target, err := TargetFromListener(listener)
	if err != nil {
		return Target{}, err
	}
	target.Session = sessionName
	return target, nil
}

func selectDirectChannel(channels []appconfig.ChannelDoc, sessionName string, sessionExplicit bool) (appconfig.ChannelDoc, string, error) {
	var direct []appconfig.ChannelDoc
	for _, ch := range channels {
		if ch.Type == "direct" {
			direct = append(direct, ch)
		}
	}
	if len(direct) == 0 {
		return appconfig.ChannelDoc{}, "", fmt.Errorf("remote: app has no direct daemon channel")
	}
	var matching []appconfig.ChannelDoc
	for _, ch := range direct {
		if channelSession(ch) == sessionName {
			matching = append(matching, ch)
		}
	}
	if len(matching) == 1 {
		ch := matching[0]
		return ch, channelSession(ch), nil
	}
	if len(matching) > 1 {
		return appconfig.ChannelDoc{}, "", fmt.Errorf("remote: multiple direct channels match session %q: %s", sessionName, channelList(matching))
	}
	if sessionExplicit {
		return appconfig.ChannelDoc{}, "", fmt.Errorf("remote: no direct channel matches session %q (available: %s)", sessionName, channelList(direct))
	}
	if len(direct) == 1 {
		ch := direct[0]
		return ch, channelSession(ch), nil
	}
	return appconfig.ChannelDoc{}, "", fmt.Errorf("remote: multiple direct channels are available; pass --session (available: %s)", channelList(direct))
}

func channelSession(ch appconfig.ChannelDoc) string {
	if strings.TrimSpace(ch.Session) != "" {
		return strings.TrimSpace(ch.Session)
	}
	return strings.TrimSpace(ch.Name)
}

func channelList(channels []appconfig.ChannelDoc) string {
	var parts []string
	for _, ch := range channels {
		parts = append(parts, fmt.Sprintf("%s session=%s listener=%s", ch.Name, channelSession(ch), ch.Listener))
	}
	sort.Strings(parts)
	return strings.Join(parts, "; ")
}

func listenerByName(listeners []appconfig.ListenerDoc, name string) (appconfig.ListenerDoc, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return appconfig.ListenerDoc{}, fmt.Errorf("remote: direct channel listener is empty")
	}
	for _, listener := range listeners {
		if listener.Name == name {
			return listener, nil
		}
	}
	return appconfig.ListenerDoc{}, fmt.Errorf("remote: listener %q not found", name)
}

func TargetFromListener(listener appconfig.ListenerDoc) (Target, error) {
	if listener.Type != "http" {
		return Target{}, fmt.Errorf("remote: listener %q uses unsupported type %q", listener.Name, listener.Type)
	}
	addr := strings.TrimSpace(listener.Addr)
	if addr == "" {
		addr = "127.0.0.1:8080"
	}
	mode := strings.ToLower(strings.TrimSpace(distserve.AuthString(listener.Auth, "mode")))
	var token string
	switch mode {
	case "":
		if distserve.AddrIsTCP(addr) {
			return Target{}, fmt.Errorf("remote: listener %q uses TCP addr %q and requires auth", listener.Name, addr)
		}
	case "local_socket":
		if distserve.AddrIsTCP(addr) {
			return Target{}, fmt.Errorf("remote: listener %q auth mode local_socket requires a unix socket addr", listener.Name)
		}
	case "bearer", "token":
		token = distserve.AuthString(listener.Auth, "token")
		if token == "" {
			if env := distserve.AuthString(listener.Auth, "env"); env != "" {
				token = os.Getenv(env)
			}
		}
		if token == "" {
			return Target{}, fmt.Errorf("remote: listener %q bearer auth token is empty", listener.Name)
		}
	default:
		return Target{}, fmt.Errorf("remote: listener %q unsupported auth mode %q", listener.Name, mode)
	}
	if distserve.AddrIsTCP(addr) {
		if strings.HasPrefix(addr, "http://") || strings.HasPrefix(addr, "https://") {
			return Target{BaseURL: strings.TrimRight(addr, "/"), BearerToken: token}, nil
		}
		return Target{BaseURL: "http://" + addr, BearerToken: token}, nil
	}
	return Target{BaseURL: "http://unix", Socket: distserve.ResolveSocketPath(addr), BearerToken: token}, nil
}

func ResolveSocketPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if filepath.IsAbs(raw) || strings.ContainsRune(raw, filepath.Separator) {
		return raw
	}
	return distserve.ResolveSocketPath(raw)
}

func terminalOptions(opts Options) terminalui.TurnOptions {
	return terminalui.TurnOptions{
		Debug: opts.Debug,
		Usage: opts.Usage,
		Out:   writerOr(opts.Out, os.Stdout),
		Err:   writerOr(opts.Err, os.Stderr),
	}
}

func readerOr(value io.Reader, fallback io.Reader) io.Reader {
	if value != nil {
		return value
	}
	return fallback
}

func writerOr(value io.Writer, fallback io.Writer) io.Writer {
	if value != nil {
		return value
	}
	return fallback
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
