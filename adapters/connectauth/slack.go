package connectauth

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/codewandler/connectors/credential"
)

// SlackCredentials are the native Slack tokens needed by the Slack channel.
type SlackCredentials struct {
	InstanceID string
	BotToken   string
	UserToken  string
	AppToken   string
	BotUserID  string
	TeamID     string
}

// Store reads connector instance and credential state from the connectors
// library's on-disk stores.
type Store struct {
	Credentials *credential.FileStore
	Instances   *credential.InstanceStore
}

// NewStore returns a connectors-compatible file store rooted at basePath.
func NewStore(basePath string) Store {
	if strings.TrimSpace(basePath) == "" {
		basePath = "~/.connectors"
	}
	basePath = expandHome(basePath)
	return Store{
		Credentials: credential.NewFileStore(filepath.Join(basePath, "credentials")),
		Instances:   credential.NewInstanceStore(filepath.Join(basePath, "instances")),
	}
}

// LoadSlack resolves one Slack connector instance. Empty instanceID selects the
// first stored instance with connector == "slack", or "slack" when present.
func (s Store) LoadSlack(ctx context.Context, instanceID string) (SlackCredentials, error) {
	if s.Credentials == nil {
		s.Credentials = credential.NewFileStore("")
	}
	if s.Instances == nil {
		s.Instances = credential.NewInstanceStore("")
	}
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		instances, err := s.Instances.ListByConnector(ctx, "slack")
		if err != nil {
			return SlackCredentials{}, err
		}
		for _, inst := range instances {
			if inst.ID == "slack" {
				instanceID = inst.ID
				break
			}
		}
		if instanceID == "" && len(instances) > 0 {
			instanceID = instances[0].ID
		}
	}
	if instanceID == "" {
		return SlackCredentials{}, fmt.Errorf("connectauth: no connected slack instance found; run agentsdk connect slack")
	}
	inst, err := s.Instances.Load(ctx, instanceID)
	if err != nil {
		return SlackCredentials{}, fmt.Errorf("connectauth: load slack instance %q: %w", instanceID, err)
	}
	if inst.Connector != "slack" {
		return SlackCredentials{}, fmt.Errorf("connectauth: instance %q is connector %q, want slack", instanceID, inst.Connector)
	}
	creds, err := s.Credentials.Load(ctx, instanceID)
	if err != nil {
		return SlackCredentials{}, fmt.Errorf("connectauth: load slack credentials %q: %w", instanceID, err)
	}
	out := SlackCredentials{
		InstanceID: instanceID,
		BotToken:   firstNonEmpty(creds.Auth.Token, creds.Fields["token"], creds.Fields["bot_token"]),
		UserToken:  creds.Fields["user_token"],
		AppToken:   creds.Fields["app_token"],
		BotUserID:  firstNonEmpty(inst.Grants["bot"].PrincipalID, creds.Fields["bot_user_id"]),
		TeamID:     firstNonEmpty(inst.Fields["team_id"], creds.Fields["team_id"]),
	}
	if out.BotToken == "" {
		return SlackCredentials{}, fmt.Errorf("connectauth: slack instance %q has no bot token", instanceID)
	}
	if out.AppToken == "" {
		return SlackCredentials{}, fmt.Errorf("connectauth: slack instance %q has no app token", instanceID)
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(path, "~/"))
		}
	}
	return path
}
