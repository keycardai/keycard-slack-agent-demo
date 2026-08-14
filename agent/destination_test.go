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
	// An ALLOWLIST, deliberately, because the denylist this replaced only banned
	// "allow_external" and "user_id" and so said nothing about "destination". That parameter
	// sat on authorize_resource with an enum including "channel", handing the model the
	// choice the DM guard exists to make, and the test stayed green the whole time. A new
	// parameter now has to be argued for here before a model can ever see it.
	//
	// slack_action may take channel and thread_ts because slackAction validates both against
	// the request context. authorize_resource takes neither: it derives everything from reqCtx.
	allowed := map[string]map[string]bool{
		ToolSlackAction: {
			"action": true, "channel": true, "thread_ts": true,
			"ts": true, "text": true, "reaction": true,
		},
		ToolAuthorizeResource: {
			"resource": true, "resources": true,
		},
	}
	for _, tool := range ToolList() {
		permitted, known := allowed[tool.Name]
		if !known {
			t.Fatalf("tool %q has no allowlist entry; add one and justify every parameter", tool.Name)
		}
		schema, ok := tool.InputSchema["properties"].(map[string]any)
		if !ok {
			continue
		}
		for name := range schema {
			if !permitted[name] {
				t.Fatalf("tool %q offers the model an un-allowlisted %q parameter", tool.Name, name)
			}
		}
	}
}

func TestAuthorizeResourceCannotBeAimedAtAChannel(t *testing.T) {
	// The regression the allowlist above exists to catch, exercised end to end: even when the
	// model asks for a channel destination, the consent link goes to a DM because the request
	// was not private. The parameters are gone from the schema, so this asserts that the
	// executor ignores them rather than trusting the schema alone to keep them away.
	slack := &recordingSlack{}
	runner := &Runner{SlackActions: slack, BaseURL: "https://agent.example.com", LinkSigner: &stubLinkSigner{}}
	authTools := map[string]AuthToolRef{
		"authorize_linear": {Resource: "https://mcp.linear.app/mcp", Label: "Linear MCP"},
	}

	result := runner.executeTool(context.Background(), channelRequest(), "token", ToolAuthorizeResource,
		map[string]any{
			"resource":    "https://mcp.linear.app/mcp",
			"destination": "channel",
			"channel":     "C-public",
		}, nil, authTools)

	if result.IsError {
		t.Fatalf("expected the consent link to be sent, got %+v", result)
	}
	// posts are recorded as "channel|text".
	for _, post := range slack.posts {
		if strings.HasPrefix(post, "C-public|") {
			t.Fatalf("a consent link reached the channel the model named: %#v", slack.posts)
		}
	}
	if len(slack.openDMs) != 1 || slack.openDMs[0] != "U-asker" {
		t.Fatalf("expected the link to go to the asker's DM, got openDMs=%#v", slack.openDMs)
	}
}

func TestTheModelCannotAuthorizeAnInventedResource(t *testing.T) {
	// Asked about a board it could not reach, a model called authorize_resource with resource="linear".
	// The link it produced failed at Keycard with "Requested authorization for unknown resource", after
	// the agent had told the user a link was on its way. A resource identifier is not the model's to
	// invent, and the only valid ones are those resolved for this request.
	slack := &recordingSlack{}
	runner := &Runner{SlackActions: slack, BaseURL: "https://agent.example.com", LinkSigner: &stubLinkSigner{}}
	authTools := map[string]AuthToolRef{
		"authorize_linear": {Resource: "https://mcp.linear.app/mcp", Label: "Linear MCP"},
	}

	result := runner.executeTool(context.Background(), channelRequest(), "token",
		ToolAuthorizeResource, map[string]any{"resource": "linear"}, nil, authTools)

	if !result.IsError {
		t.Fatalf("expected an invented identifier to be refused, got %+v", result)
	}
	// The refusal has to hand back the real names, or the model has no way to correct itself.
	if !strings.Contains(result.Content, "https://mcp.linear.app/mcp") {
		t.Fatalf("the refusal should name the available identifiers: %q", result.Content)
	}
	if len(slack.posts) != 0 {
		t.Fatalf("a link was sent for an unknown resource: %#v", slack.posts)
	}
}

func TestARealResourceIsStillAuthorizable(t *testing.T) {
	slack := &recordingSlack{}
	runner := &Runner{SlackActions: slack, BaseURL: "https://agent.example.com", LinkSigner: &stubLinkSigner{}}
	authTools := map[string]AuthToolRef{
		"authorize_linear": {Resource: "https://mcp.linear.app/mcp", Label: "Linear MCP"},
	}

	result := runner.executeTool(context.Background(), channelRequest(), "token",
		ToolAuthorizeResource, map[string]any{"resource": "https://mcp.linear.app/mcp"}, nil, authTools)

	if result.IsError {
		t.Fatalf("expected a known resource to be authorizable, got %+v", result)
	}
}

func TestWithNothingResolvedTheModelIsToldToSaySo(t *testing.T) {
	// The state a real run was in: discovery failed, so nothing is resolved. Guessing is the wrong
	// response and the tool result says which response is right.
	slack := &recordingSlack{}
	runner := &Runner{SlackActions: slack, BaseURL: "https://agent.example.com", LinkSigner: &stubLinkSigner{}}

	result := runner.executeTool(context.Background(), channelRequest(), "token",
		ToolAuthorizeResource, map[string]any{"resource": "linear"}, nil, nil)

	if !result.IsError {
		t.Fatalf("expected a refusal, got %+v", result)
	}
	if !strings.Contains(result.Content, "nothing to") {
		t.Fatalf("unexpected content: %q", result.Content)
	}
	// The wording must not send the person to an administrator. A model read the previous message as a
	// per-user permission problem and told them their identity needed provisioning; it was already fine,
	// and the agent simply could not read its own configuration.
	if !strings.Contains(result.Content, "not a problem with this person's account") {
		t.Fatalf("the refusal should rule out a user-provisioning problem: %q", result.Content)
	}
}
