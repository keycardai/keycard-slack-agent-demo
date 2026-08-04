package slackapp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/slack-go/slack"

	"github.com/keycardai/keycard-slack-agent-demo/agent"
	"github.com/keycardai/keycard-slack-agent-demo/app"
	"github.com/keycardai/keycard-slack-agent-demo/config"
	"github.com/keycardai/keycard-slack-agent-demo/db"
	"github.com/keycardai/keycard-slack-agent-demo/oauth"
)

const keycardConsentPrompt = "keycard_consent_link"

// OAuthClient is the subset of *oauth.Client that Handler uses.
// Extracted to allow test doubles without a real Keycard zone.
type OAuthClient interface {
	RefreshTokens(ctx context.Context, refreshToken string) (*oauth.TokenResponse, error)
	FetchUserInfo(ctx context.Context, accessToken string) (*oauth.UserInfo, error)
}

type Handler struct {
	Config  config.Config
	DB      *db.DB
	Agent   *agent.Runner
	OAuth   OAuthClient
	Slack   *slack.Client
	BaseURL string

	botUserID   string
	botUserMu   sync.Mutex
	reactionMu  sync.Mutex
	handledKeys map[string]time.Time

	// LinkSigner signs consent links so a link cannot be forged by whoever receives one.
	LinkSigner oauth.LinkSigner

	// runAgentFunc allows unit tests to observe trigger decisions without invoking the full runner.
	runAgentFunc func(teamID, userID, channelID, channelType, text, threadTS, messageTS string)
}

func NewHandler(cfg config.Config, database *db.DB, agentRunner *agent.Runner, oauthClient OAuthClient, slackClient *slack.Client, baseURL string) *Handler {
	return &Handler{
		Config:      cfg,
		DB:          database,
		Agent:       agentRunner,
		OAuth:       oauthClient,
		Slack:       slackClient,
		BaseURL:     baseURL,
		handledKeys: make(map[string]time.Time),
	}
}

func (h *Handler) HandleMessage(teamID, userID, channelID, channelType, text, threadTS, messageTS string) {
	ctx := context.Background()
	_, isPrivate := classifyChat(channelID, channelType)

	// DMs are always handled.
	if isPrivate {
		if !h.markHandled(channelID, messageTS) {
			return
		}
		h.callRunAgent(teamID, userID, channelID, channelType, text, threadTS, messageTS)
		return
	}

	// @-mention always triggers a response.
	if h.messageMentionsBot(ctx, text) {
		if !h.markHandled(channelID, messageTS) {
			return
		}
		h.callRunAgent(teamID, userID, channelID, channelType, text, threadTS, messageTS)
		return
	}

	// In threads with an existing session, ask Haiku whether to respond.
	if strings.TrimSpace(threadTS) != "" && h.DB != nil {
		sessionID := app.MakeSessionID(teamID, channelID, threadTS)
		if exists, _ := h.DB.SessionExists(sessionID); exists {
			if h.haikuGate(ctx, channelID, threadTS, text) {
				if !h.markHandled(channelID, messageTS) {
					return
				}
				h.callRunAgent(teamID, userID, channelID, channelType, text, threadTS, messageTS)
				return
			}
		}
	}
}

func (h *Handler) runAgent(teamID, userID, channelID, channelType, text, threadTS, messageTS string) {
	ctx := context.Background()
	chatType, isPrivate := classifyChat(channelID, channelType)

	tokens, err := h.DB.GetUserTokens(teamID, userID)
	if err != nil || tokens == nil {
		h.sendLinkPrompt(channelID, userID, teamID, threadTS)
		return
	}
	tokenFP := app.TokenFingerprint(tokens.AccessToken)
	if tokenFP == "" {
		log.Printf("keycard token loaded: team=%s user=%s fp=missing expires_at=%d", teamID, userID, tokens.ExpiresAt)
	} else {
		log.Printf("keycard token loaded: team=%s user=%s fp=%s expires_at=%d", teamID, userID, tokenFP, tokens.ExpiresAt)
	}

	if tokens.ExpiresAt < time.Now().Unix() {
		refreshed, err := h.OAuth.RefreshTokens(ctx, tokens.RefreshToken)
		if err != nil {
			if err == oauth.ErrInvalidGrant {
				_ = h.DB.DeleteUserTokens(teamID, userID)
				h.sendLinkPrompt(channelID, userID, teamID, threadTS)
			} else {
				_, _ = h.sendMessage(channelID, "Temporary error, please try again.", threadTS)
			}
			return
		}
		tokens.AccessToken = refreshed.AccessToken
		if refreshed.RefreshToken != "" {
			tokens.RefreshToken = refreshed.RefreshToken
		}
		tokens.ExpiresAt = time.Now().Unix() + refreshed.ExpiresIn
		_ = h.DB.StoreUserTokens(teamID, userID, tokens)
		refreshedFP := app.TokenFingerprint(tokens.AccessToken)
		if refreshedFP == "" {
			log.Printf("keycard token refreshed: team=%s user=%s fp=missing expires_at=%d", teamID, userID, tokens.ExpiresAt)
		} else {
			log.Printf("keycard token refreshed: team=%s user=%s fp=%s expires_at=%d", teamID, userID, refreshedFP, tokens.ExpiresAt)
		}
	}

	identity := tokens.KeycardIdentity
	if identity == "" {
		info, err := h.OAuth.FetchUserInfo(ctx, tokens.AccessToken)
		if err != nil || info == nil || info.Sub == "" {
			_ = h.DB.DeleteUserTokens(teamID, userID)
			h.sendLinkPrompt(channelID, userID, teamID, threadTS)
			return
		}
		identity = info.Sub
		tokens.KeycardIdentity = info.Sub
		_ = h.DB.StoreUserTokens(teamID, userID, tokens)
	}
	if identity == "" {
		// Never invent one. A synthesised "team-user" string would be accepted by everything
		// downstream while naming nobody Keycard has heard of, which is the one failure this
		// agent must not have.
		log.Printf("keycard identity missing after userinfo: team=%s user=%s", teamID, userID)
		_ = h.DB.DeleteUserTokens(teamID, userID)
		h.sendLinkPrompt(channelID, userID, teamID, threadTS)
		return
	}

	sessionID := app.MakeSessionID(teamID, channelID, threadTS)
	reqCtx := &app.RequestContext{
		KeycardIdentity: identity,
		BaseURL:         h.BaseURL,
		TeamID:          teamID,
		UserID:          userID,
		ChannelID:       channelID,
		ThreadTS:        threadTS,
		MessageTS:       messageTS,
		SessionID:       sessionID,
		ChatType:        chatType,
		IsPrivate:       isPrivate,
	}

	if err := h.ensureSessionForContext(teamID, channelID, threadTS, isPrivate); err != nil {
		_, _ = h.sendMessage(channelID, "Failed to start session.", threadTS)
		return
	}

	// Strip the bot @-mention from the user message — it's noise in the prompt.
	text = h.stripBotMention(ctx, text)

	// For non-private threads, fetch the full Slack thread so the agent sees
	// the complete conversation, not just its own DB history.
	if !isPrivate && strings.TrimSpace(threadTS) != "" {
		if preamble := h.fetchThreadContext(ctx, channelID, threadTS); preamble != "" {
			text = preamble + "\n" + text
		}
	}

	_ = h.DB.AddMessage(sessionID, "user", text, userID, messageTS)
	_ = h.DB.UpsertSessionIndex(db.SessionIndex{
		SessionID:     sessionID,
		TeamID:        teamID,
		ChannelID:     channelID,
		ThreadTS:      threadTS,
		UserID:        userID,
		IsPrivate:     isPrivate,
		LastMessageTS: messageTS,
		LastSeenAt:    time.Now().Unix(),
	})

	// No warm goroutine. The request resolves discovery, the exchange, and the tool list itself, so a
	// warm running alongside it duplicated every exchange and won nothing: the log showed each one twice.

	responseChan := make(chan agent.AgentEvent)
	go h.Agent.Run(ctx, reqCtx, tokens.AccessToken, text, responseChan)

	var response strings.Builder
	needSeparator := false
	lastCharNewline := false
	for event := range responseChan {
		switch event.Type {
		case agent.EventMessageStart:
			if event.MessageRole == "assistant" && response.Len() > 0 {
				needSeparator = true
			}
		case agent.EventText:
			if needSeparator {
				if response.Len() > 0 && !lastCharNewline {
					response.WriteString("\n")
					lastCharNewline = true
				}
				needSeparator = false
			}
			response.WriteString(event.Content)
			if len(event.Content) > 0 {
				lastCharNewline = event.Content[len(event.Content)-1] == '\n'
			}
		case agent.EventDone:
			final := response.String()
			if strings.TrimSpace(final) == "" {
				log.Printf("agent reply skipped: session=%s empty response", sessionID)
				continue
			}
			log.Printf("agent reply: session=%s chars=%d preview=%q", sessionID, len(final), truncateLog(final, 240))
			ts, err := h.sendMessage(channelID, final, threadTS)
			if err == nil {
				_ = h.DB.AddMessage(sessionID, "assistant", final, "", ts)
			}
		case agent.EventError:
			_, _ = h.sendMessage(channelID, "Error: "+event.Content, threadTS)
		}
	}
}

func (h *Handler) callRunAgent(teamID, userID, channelID, channelType, text, threadTS, messageTS string) {
	if h.runAgentFunc != nil {
		h.runAgentFunc(teamID, userID, channelID, channelType, text, threadTS, messageTS)
		return
	}
	h.runAgent(teamID, userID, channelID, channelType, text, threadTS, messageTS)
}

func (h *Handler) shouldHandleMessage(ctx context.Context, channelID, channelType, text, threadTS, messageTS string) bool {
	_, isPrivate := classifyChat(channelID, channelType)
	if isPrivate {
		return true
	}
	return h.messageMentionsBot(ctx, text)
}

const (
	messageHandledTTL        = 24 * time.Hour
	messageHandledCleanupTTL = 48 * time.Hour
	haikuGateTimeout         = 10 * time.Second
)

const haikuGatePrompt = `You are a routing classifier for a Slack bot. Given a thread conversation and a new message, decide whether the bot should respond. In the transcript, the bot's messages are labeled "bot".

Respond "yes" if:
- The message is a question or request directed at the bot
- The message is a follow-up to something the bot said (e.g. answering the bot's question)
- The message asks for clarification or more detail on the bot's previous response

Respond "no" if:
- The message is users talking to each other, not the bot
- The message is a simple acknowledgment like "thanks" or "ok" with no further ask
- The message is unrelated side conversation

Reply with a single word: yes or no.`

func (h *Handler) messageMentionsBot(ctx context.Context, text string) bool {
	botUserID, err := h.getBotUserID(ctx)
	if err != nil || botUserID == "" {
		if err != nil {
			log.Printf("bot user lookup failed: %v", err)
		}
		return false
	}
	mention := "<@" + botUserID + ">"
	return strings.Contains(text, mention)
}

func (h *Handler) stripBotMention(ctx context.Context, text string) string {
	botUserID, err := h.getBotUserID(ctx)
	if err != nil || botUserID == "" {
		return text
	}
	mention := "<@" + botUserID + ">"
	text = strings.ReplaceAll(text, mention, "")
	return strings.TrimSpace(text)
}

func (h *Handler) fetchThreadContext(ctx context.Context, channelID, threadTS string) string {
	if h.Slack == nil {
		return ""
	}
	botUserID, err := h.getBotUserID(ctx)
	if err != nil {
		return ""
	}
	params := slack.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTS,
		Limit:     200,
		Inclusive: true,
	}
	messages, _, _, err := h.Slack.GetConversationRepliesContext(ctx, &params)
	if err != nil {
		log.Printf("thread context fetch failed: channel=%s ts=%s err=%v", channelID, threadTS, err)
		return ""
	}
	if len(messages) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("[thread context]\n")
	for _, msg := range messages {
		author := msg.User
		if author == botUserID {
			author = "bot"
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", author, msg.Text))
	}
	sb.WriteString("[end thread context]")
	return sb.String()
}

func (h *Handler) haikuGate(ctx context.Context, channelID, threadTS, newMessage string) bool {
	if h.Agent == nil || h.Agent.Client == nil {
		return false
	}
	threadContext := h.fetchThreadContext(ctx, channelID, threadTS)
	if threadContext == "" {
		return false
	}
	prompt := threadContext + "\n\nNew message: " + newMessage

	callCtx, cancel := context.WithTimeout(ctx, haikuGateTimeout)
	defer cancel()

	resp, err := h.Agent.Client.Messages.New(callCtx, anthropic.MessageNewParams{
		Model:     anthropic.Model("claude-haiku-4-5-20251001"),
		MaxTokens: 4,
		System:    []anthropic.TextBlockParam{{Text: haikuGatePrompt}},
		Messages: []anthropic.MessageParam{
			{
				Role:    anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(prompt)},
			},
		},
	})
	if err != nil {
		log.Printf("haiku gate error: channel=%s thread=%s err=%v", channelID, threadTS, err)
		return false
	}

	for _, block := range resp.Content {
		if block.Type == "text" {
			answer := strings.TrimSpace(strings.ToLower(block.Text))
			log.Printf("haiku gate: channel=%s thread=%s answer=%q", channelID, threadTS, answer)
			return strings.HasPrefix(answer, "yes")
		}
	}
	return false
}

func (h *Handler) getBotUserID(ctx context.Context) (string, error) {
	h.botUserMu.Lock()
	defer h.botUserMu.Unlock()
	if h.botUserID != "" {
		return h.botUserID, nil
	}
	if h.Slack == nil {
		return "", errors.New("missing slack client")
	}
	resp, err := h.Slack.AuthTestContext(ctx)
	if err != nil {
		return "", err
	}
	botUserID := strings.TrimSpace(resp.UserID)
	if botUserID == "" {
		return "", errors.New("missing bot user id")
	}
	h.botUserID = botUserID
	return botUserID, nil
}

func (h *Handler) markHandled(channelID, messageTS string) bool {
	if channelID == "" || messageTS == "" {
		return true
	}
	key := channelID + ":" + messageTS
	now := time.Now()
	h.reactionMu.Lock()
	defer h.reactionMu.Unlock()
	if seenAt, ok := h.handledKeys[key]; ok {
		if now.Sub(seenAt) < messageHandledTTL {
			return false
		}
	}
	h.handledKeys[key] = now
	for k, seenAt := range h.handledKeys {
		if now.Sub(seenAt) > messageHandledCleanupTTL {
			delete(h.handledKeys, k)
		}
	}
	return true
}

func classifyChat(channelID, channelType string) (string, bool) {
	if channelType == "im" || strings.HasPrefix(channelID, "D") {
		return "private", true
	}
	return "public", false
}

func (h *Handler) sendLinkPrompt(channelID, userID, teamID, threadTS string) {
	_, isPrivate := classifyChat(channelID, "")

	// A person in a DM is unmistakably addressing the agent, so they get told why nothing happened.
	// The short window only stops a burst of identical messages producing a burst of identical DMs.
	cooldown := db.AuthPromptCooldown
	if isPrivate {
		cooldown = db.AuthPromptDirectCooldown
	}
	if h.DB != nil {
		sent, err := h.DB.HasAuthPromptSentWithin(teamID, userID, keycardConsentPrompt, cooldown)
		if err == nil && sent {
			return
		}
		if err != nil {
			log.Printf("auth prompt lookup failed: team=%s user=%s err=%v", teamID, userID, err)
		}
	}
	if h.LinkSigner == nil {
		log.Printf("consent prompt skipped: team=%s user=%s reason=no_signer", teamID, userID)
		return
	}
	signed, err := h.LinkSigner.SignLink(oauth.LinkRequest{TeamID: teamID, UserID: userID, ChannelID: channelID})
	if err != nil {
		log.Printf("consent link signing failed: team=%s user=%s err=%v", teamID, userID, err)
		return
	}
	link := fmt.Sprintf("%s/oauth/start?t=%s", h.BaseURL, url.QueryEscape(signed))
	message := fmt.Sprintf(
		"Before I can do anything for you I need to know who you are in Keycard, so I can act with "+
			"your access rather than my own. Connect your account here: %s",
		link,
	)
	sent := false
	if isPrivate {
		if ts, err := h.sendMessage(channelID, message, threadTS); err == nil && ts != "" {
			sent = true
		}
	} else if h.Slack != nil {
		params := &slack.OpenConversationParameters{Users: []string{userID}}
		dm, _, _, err := h.Slack.OpenConversationContext(context.Background(), params)
		if err == nil && dm.ID != "" {
			if ts, err := h.sendMessage(dm.ID, message, ""); err == nil && ts != "" {
				sent = true
			}
		}
		if err != nil {
			log.Printf("sendLinkPrompt dm failed: channel=%s user=%s err=%v", channelID, userID, err)
		}
	}
	if sent && h.DB != nil {
		if err := h.DB.MarkAuthPromptSent(teamID, userID, keycardConsentPrompt); err != nil {
			log.Printf("auth prompt mark failed: team=%s user=%s err=%v", teamID, userID, err)
		}
	}
}

func (h *Handler) sendMessage(channelID, text, threadTS string) (string, error) {
	text = normalizeSlackMarkdown(text)
	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	_, ts, err := h.Slack.PostMessage(channelID, opts...)
	return ts, err
}

func normalizeSlackMarkdown(text string) string {
	if text == "" {
		return text
	}
	text = convertMarkdownTables(text)
	if !strings.ContainsAny(text, "*_[]()#") {
		return text
	}
	var out strings.Builder
	out.Grow(len(text))
	inCodeBlock := false
	inInline := false
	for i := 0; i < len(text); i++ {
		if !inInline && strings.HasPrefix(text[i:], "```") {
			inCodeBlock = !inCodeBlock
			out.WriteString("```")
			i += 2
			continue
		}
		ch := text[i]
		if !inCodeBlock {
			if ch == '`' {
				inInline = !inInline
				out.WriteByte(ch)
				continue
			}
			if !inInline && ch == '[' {
				if converted, advance := convertMarkdownLink(text[i:]); advance > 0 {
					out.WriteString(converted)
					i += advance - 1
					continue
				}
			}
			if !inInline && ch == '#' && (i == 0 || text[i-1] == '\n') {
				if converted, advance := convertMarkdownHeading(text[i:]); advance > 0 {
					out.WriteString(converted)
					i += advance - 1
					continue
				}
			}
			if !inInline && i+1 < len(text) {
				if text[i] == '*' && text[i+1] == '*' {
					if converted, advance := convertMarkdownBold(text[i:], "**"); advance > 0 {
						out.WriteString(converted)
						i += advance - 1
						continue
					}
				}
				if text[i] == '_' && text[i+1] == '_' {
					if converted, advance := convertMarkdownBold(text[i:], "__"); advance > 0 {
						out.WriteString(converted)
						i += advance - 1
						continue
					}
				}
			}
			if !inInline && (ch == '*' || ch == '_') {
				if converted, advance := convertMarkdownItalic(text, i); advance > 0 {
					out.WriteString(converted)
					i += advance - 1
					continue
				}
			}
		}
		out.WriteByte(ch)
	}
	return out.String()
}

func convertMarkdownTables(text string) string {
	if text == "" {
		return text
	}
	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))
	inCodeBlock := false
	for i := 0; i < len(lines); {
		line := lines[i]
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
			out = append(out, line)
			i++
			continue
		}
		if inCodeBlock {
			out = append(out, line)
			i++
			continue
		}
		if i+1 < len(lines) && looksLikeTableHeader(line) && looksLikeTableSeparator(lines[i+1]) {
			header := splitTableRow(line)
			if len(header) < 2 {
				out = append(out, line)
				i++
				continue
			}
			rows := [][]string{}
			j := i + 2
			for j < len(lines) {
				row := lines[j]
				if row == "" || strings.HasPrefix(row, "```") {
					break
				}
				if !looksLikeTableRow(row) {
					break
				}
				parsed := splitTableRow(row)
				if len(parsed) < 2 {
					break
				}
				rows = append(rows, parsed)
				j++
			}
			if len(rows) == 0 {
				out = append(out, line)
				i++
				continue
			}
			out = append(out, formatTableHeader(header))
			for _, row := range rows {
				out = append(out, formatTableRow(row))
			}
			i = j
			continue
		}
		out = append(out, line)
		i++
	}
	return strings.Join(out, "\n")
}

func looksLikeTableHeader(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && strings.Contains(trimmed, "|")
}

func looksLikeTableSeparator(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.Contains(trimmed, "|") || !strings.Contains(trimmed, "-") {
		return false
	}
	for _, r := range trimmed {
		switch r {
		case '|', '-', ':', ' ':
			continue
		default:
			return false
		}
	}
	return true
}

func looksLikeTableRow(line string) bool {
	trimmed := strings.TrimSpace(line)
	return trimmed != "" && strings.Contains(trimmed, "|")
}

func splitTableRow(line string) []string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "|")
	trimmed = strings.TrimSuffix(trimmed, "|")
	parts := strings.Split(trimmed, "|")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		cell := strings.TrimSpace(part)
		if cell == "" {
			out = append(out, cell)
			continue
		}
		out = append(out, cell)
	}
	return out
}

func formatTableHeader(cells []string) string {
	if len(cells) < 2 {
		return ""
	}
	left := strings.TrimSpace(cells[0])
	right := strings.TrimSpace(strings.Join(cells[1:], " | "))
	if left == "" && right == "" {
		return ""
	}
	if right == "" {
		return "*" + left + "*"
	}
	if left == "" {
		return "*" + right + "*"
	}
	return "*" + left + "* - *" + right + "*"
}

func formatTableRow(cells []string) string {
	if len(cells) < 2 {
		return strings.TrimSpace(strings.Join(cells, " "))
	}
	left := strings.TrimSpace(cells[0])
	right := strings.TrimSpace(strings.Join(cells[1:], " | "))
	if right == "" {
		return left
	}
	if left == "" {
		return right
	}
	return left + " - " + right
}

func convertMarkdownLink(text string) (string, int) {
	if len(text) == 0 || text[0] != '[' {
		return "", 0
	}
	endText := strings.IndexByte(text, ']')
	if endText <= 0 || endText+1 >= len(text) || text[endText+1] != '(' {
		return "", 0
	}
	endURL := strings.IndexByte(text[endText+2:], ')')
	if endURL < 0 {
		return "", 0
	}
	label := strings.TrimSpace(text[1:endText])
	url := strings.TrimSpace(text[endText+2 : endText+2+endURL])
	if label == "" || url == "" {
		return "", 0
	}
	return fmt.Sprintf("<%s|%s>", url, label), endText + 2 + endURL + 1
}

func convertMarkdownHeading(text string) (string, int) {
	i := 0
	for i < len(text) && text[i] == '#' {
		i++
	}
	if i == 0 || i >= len(text) || text[i] != ' ' {
		return "", 0
	}
	lineEnd := strings.IndexByte(text, '\n')
	if lineEnd < 0 {
		lineEnd = len(text)
	}
	content := strings.TrimSpace(text[i:lineEnd])
	if content == "" {
		return "", 0
	}
	return fmt.Sprintf("*%s*", content), lineEnd
}

func convertMarkdownBold(text, token string) (string, int) {
	if !strings.HasPrefix(text, token) {
		return "", 0
	}
	end := strings.Index(text[len(token):], token)
	if end <= 0 {
		return "", 0
	}
	content := text[len(token) : len(token)+end]
	if strings.TrimSpace(content) == "" {
		return "", 0
	}
	return fmt.Sprintf("*%s*", content), len(token) + end + len(token)
}

func convertMarkdownItalic(text string, pos int) (string, int) {
	if pos < 0 || pos >= len(text) {
		return "", 0
	}
	token := text[pos]
	if token != '*' && token != '_' {
		return "", 0
	}
	if pos+1 < len(text) && text[pos+1] == token {
		return "", 0
	}
	if (pos == 0 || text[pos-1] == '\n') && pos+1 < len(text) && text[pos+1] == ' ' {
		return "", 0
	}
	end := strings.IndexByte(text[pos+1:], token)
	if end <= 0 {
		return "", 0
	}
	content := text[pos+1 : pos+1+end]
	if strings.TrimSpace(content) == "" {
		return "", 0
	}
	return fmt.Sprintf("_%s_", content), end + 2
}

func (h *Handler) ensureSessionForContext(teamID, channelID, threadTS string, isPrivate bool) error {
	sessionID := app.MakeSessionID(teamID, channelID, threadTS)
	if isPrivate || threadTS == "" {
		_, err := h.DB.GetOrCreateSession(sessionID, teamID, channelID, threadTS)
		return err
	}

	parentID := app.MakeSessionID(teamID, channelID, "")
	if _, err := h.DB.GetOrCreateSession(parentID, teamID, channelID, ""); err != nil {
		return err
	}
	if _, err := h.DB.GetOrCreateSession(sessionID, teamID, channelID, threadTS); err != nil {
		return err
	}

	threadContext, err := h.DB.GetContext(sessionID)
	if err != nil {
		return err
	}
	if len(threadContext) > 0 {
		return nil
	}

	parentContext, err := h.DB.GetContext(parentID)
	if err != nil {
		return err
	}
	if len(parentContext) == 0 {
		return nil
	}

	return h.DB.SaveContext(sessionID, parentContext)
}

func truncateLog(value string, max int) string {
	trimmed := strings.TrimSpace(value)
	if max <= 0 || len(trimmed) <= max {
		return trimmed
	}
	return trimmed[:max] + "…"
}

// HandleUserDeactivated forgets a person's stored session when Slack says their account is gone.
//
// Holding a Keycard refresh token for a deactivated account is the standing credential this whole
// design exists to avoid, and nothing else in the system would ever clean it up.
func (h *Handler) HandleUserDeactivated(teamID, userID string) {
	if h.DB == nil || teamID == "" || userID == "" {
		return
	}
	if err := h.DB.DeleteUserTokens(teamID, userID); err != nil {
		log.Printf("deactivation cleanup failed: team=%s user=%s err=%v", teamID, userID, err)
		return
	}
	log.Printf("deactivation cleanup: team=%s user=%s", teamID, userID)
}

// HandleWorkspaceRevoked forgets every stored session for a workspace, on uninstall or token
// revocation. Both mean we have lost the right to hold them.
func (h *Handler) HandleWorkspaceRevoked(teamID, reason string) {
	if h.DB == nil || teamID == "" {
		return
	}
	removed, err := h.DB.DeleteTeamTokens(teamID)
	if err != nil {
		log.Printf("workspace revocation cleanup failed: team=%s reason=%s err=%v", teamID, reason, err)
		return
	}
	log.Printf("workspace revocation cleanup: team=%s reason=%s sessions_removed=%d", teamID, reason, removed)
}
