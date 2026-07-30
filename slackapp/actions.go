package slackapp

import (
	"context"

	"github.com/slack-go/slack"
)

// ActionClient provides Slack message actions for the agent.
type ActionClient struct {
	Slack *slack.Client
}

func (a *ActionClient) OpenDM(ctx context.Context, userID string) (string, error) {
	params := &slack.OpenConversationParameters{Users: []string{userID}}
	channel, _, _, err := a.Slack.OpenConversationContext(ctx, params)
	if err != nil {
		return "", err
	}
	return channel.ID, nil
}

func (a *ActionClient) PostMessage(channel, text, threadTS string) (string, error) {
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	_, ts, err := a.Slack.PostMessage(channel, opts...)
	return ts, err
}

func (a *ActionClient) UpdateMessage(channel, ts, text string) error {
	_, _, _, err := a.Slack.UpdateMessage(channel, ts, slack.MsgOptionText(text, false))
	return err
}

func (a *ActionClient) DeleteMessage(channel, ts string) error {
	_, _, err := a.Slack.DeleteMessage(channel, ts)
	return err
}

func (a *ActionClient) AddReaction(channel, ts, reaction string) error {
	item := slack.ItemRef{Channel: channel, Timestamp: ts}
	return a.Slack.AddReaction(reaction, item)
}
