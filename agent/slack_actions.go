package agent

import "context"

// SlackActioner provides Slack message actions for the agent tool.
type SlackActioner interface {
	OpenDM(ctx context.Context, userID string) (string, error)
	PostMessage(channel, text, threadTS string) (string, error)
	UpdateMessage(channel, ts, text string) error
	DeleteMessage(channel, ts string) error
	AddReaction(channel, ts, reaction string) error
}
