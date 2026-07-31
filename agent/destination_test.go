package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/keycardai/keycard-slack-agent-demo/app"
)

// recordingSlack captures where the agent tried to write.
type recordingSlack struct {
	posts   []string // "channel|text"
	openDMs []string
}

func (r *recordingSlack) OpenDM(_ context.Context, userID string) (string, error) {
	r.openDMs = append(r.openDMs, userID)
	return "D-" + userID, nil
}

func (r *recordingSlack) PostMessage(channel, text, _ string) (string, error) {
	r.posts = append(r.posts, channel+"|"+text)
	return "ts-1", nil
}

func (r *recordingSlack) UpdateMessage(_, _, _ string) error { return nil }
func (r *recordingSlack) DeleteMessage(_, _ string) error    { return nil }
func (r *recordingSlack) AddReaction(_, _, _ string) error   { return nil }

func channelRequest() *app.RequestContext {
	return &app.RequestContext{
		TeamID:    "T1",
		UserID:    "U-asker",
		ChannelID: "C-origin",
		SessionID: "slack:T1:C-origin",
		ChatType:  "public",
	}
}

func runSlackAction(t *testing.T, reqCtx *app.RequestContext, input map[string]any) (ToolResult, *recordingSlack) {
	t.Helper()
	slack := &recordingSlack{}
	runner := &Runner{SlackActions: slack}
	result := runner.executeTool(context.Background(), reqCtx, "token", ToolSlackAction, input, nil, nil)
	return result, slack
}

func TestTheModelCannotPostToAnotherChannel(t *testing.T) {
	// This used to be waivable with an allow_external flag in the tool input, which means the guard
	// was only as trustworthy as the model it existed to guard against. In a system built on the
	// assumption that the model can be fooled, a model-set flag is not a control.
	result, slack := runSlackAction(t, channelRequest(), map[string]any{
		"action":  "send",
		"channel": "C-somewhere-else",
		"text":    "exfiltrated",
	})
	if !result.IsError {
		t.Fatalf("expected the cross-channel post to be refused, got %+v", result)
	}
	if len(slack.posts) != 0 {
		t.Fatalf("a post reached Slack anyway: %#v", slack.posts)
	}
}

func TestTheAllowExternalEscapeHatchIsGone(t *testing.T) {
	// Belt and braces: the old flag name must not resurrect the behaviour if a model guesses it.
	result, slack := runSlackAction(t, channelRequest(), map[string]any{
		"action":         "send",
		"channel":        "C-somewhere-else",
		"text":           "exfiltrated",
		"allow_external": true,
	})
	if !result.IsError {
		t.Fatalf("expected the flag to be ignored and the post refused, got %+v", result)
	}
	if len(slack.posts) != 0 {
		t.Fatalf("a post reached Slack anyway: %#v", slack.posts)
	}
}

func TestPostingBackToTheOriginChannelIsAllowed(t *testing.T) {
	result, slack := runSlackAction(t, channelRequest(), map[string]any{
		"action":  "send",
		"channel": "C-origin",
		"text":    "here is your answer",
	})
	if result.IsError {
		t.Fatalf("expected the origin channel to be allowed, got %+v", result)
	}
	if len(slack.posts) != 1 || !strings.HasPrefix(slack.posts[0], "C-origin|") {
		t.Fatalf("unexpected posts: %#v", slack.posts)
	}
}

func TestAnOmittedChannelDefaultsToWhereTheRequestCameFrom(t *testing.T) {
	result, slack := runSlackAction(t, channelRequest(), map[string]any{
		"action": "send",
		"text":   "here is your answer",
	})
	if result.IsError {
		t.Fatalf("expected an omitted channel to default to the origin, got %+v", result)
	}
	if len(slack.posts) != 1 || !strings.HasPrefix(slack.posts[0], "C-origin|") {
		t.Fatalf("unexpected posts: %#v", slack.posts)
	}
}

func TestADMAlwaysGoesToThePersonWhoAsked(t *testing.T) {
	// The recipient used to come from the tool input, so a fooled model could send its answer to
	// somebody who never asked. That is the disclosure problem with a smaller audience, not a fix.
	result, slack := runSlackAction(t, channelRequest(), map[string]any{
		"action":  "dm",
		"text":    "confidential",
		"user_id": "U-someone-else",
	})
	if result.IsError {
		t.Fatalf("expected the DM to be sent to the requester, got %+v", result)
	}
	if len(slack.openDMs) != 1 || slack.openDMs[0] != "U-asker" {
		t.Fatalf("DM was opened with the wrong user: %#v", slack.openDMs)
	}
	for _, post := range slack.posts {
		if strings.Contains(post, "U-someone-else") {
			t.Fatalf("a DM reached a user who did not ask: %#v", slack.posts)
		}
	}
}

func TestADMCannotBeUsedToReachAChannel(t *testing.T) {
	// The pre-existing guard, kept and now covered: a private conversation must not become a lever
	// for writing somewhere public.
	dmRequest := &app.RequestContext{
		TeamID:    "T1",
		UserID:    "U-asker",
		ChannelID: "D-private",
		SessionID: "slack:T1:D-private",
		ChatType:  "private",
		IsPrivate: true,
	}
	result, slack := runSlackAction(t, dmRequest, map[string]any{
		"action":  "send",
		"channel": "C-public",
		"text":    "leaked from a DM",
	})
	if !result.IsError {
		t.Fatalf("expected a DM to be unable to write into a channel, got %+v", result)
	}
	if len(slack.posts) != 0 {
		t.Fatalf("a post reached Slack anyway: %#v", slack.posts)
	}
}

func TestTheToolSchemaDoesNotOfferADestinationOverride(t *testing.T) {
	// If the parameters come back, the guards above become advisory again.
	for _, tool := range ToolList() {
		schema, ok := tool.InputSchema["properties"].(map[string]any)
		if !ok {
			continue
		}
		for _, banned := range []string{"allow_external", "user_id"} {
			if _, present := schema[banned]; present {
				t.Fatalf("tool %q offers the model a %q parameter", tool.Name, banned)
			}
		}
	}
}
