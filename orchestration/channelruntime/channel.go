package channelruntime

import (
	"context"

	clientapi "github.com/fluxplane/agentruntime/orchestration/client"
)

// Channel is a long-running external interaction surface driven by a channel
// client. Implementations own protocol IO and use the client for runtime turns.
type Channel interface {
	Name() string
	Start(context.Context, clientapi.ChannelClient) error
}
