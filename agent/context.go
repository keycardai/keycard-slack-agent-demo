package agent

import (
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/keycardai/slack-agent/app"
	"github.com/keycardai/slack-agent/prompts"
)

func BuildMessages(reqCtx *app.RequestContext, history []app.StoredMessage, userMessage string, tools []ToolSpec, systemTemplate string) ([]anthropic.MessageParam, string) {
	system := BuildSystemPrompt(reqCtx, tools, systemTemplate)

	messages := make([]anthropic.MessageParam, 0, len(history)+1)
	for _, msg := range history {
		messages = append(messages, anthropic.MessageParam{
			Role:    toAnthropicRole(msg.Role),
			Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(msg.Content)},
		})
	}
	messages = append(messages, anthropic.MessageParam{
		Role:    anthropic.MessageParamRoleUser,
		Content: []anthropic.ContentBlockParamUnion{anthropic.NewTextBlock(userMessage)},
	})

	return messages, system
}

func toAnthropicRole(role string) anthropic.MessageParamRole {
	switch role {
	case "assistant":
		return anthropic.MessageParamRoleAssistant
	default:
		return anthropic.MessageParamRoleUser
	}
}

func BuildSystemPrompt(reqCtx *app.RequestContext, tools []ToolSpec, systemTemplate string) string {
	if strings.TrimSpace(systemTemplate) == "" {
		systemTemplate = prompts.System
	}
	data := map[string]string{
		"KEYCARD_IDENTITY": reqCtx.KeycardIdentity,
		"TEAM_ID":          reqCtx.TeamID,
		"USER_ID":          reqCtx.UserID,
		"CHANNEL_ID":       reqCtx.ChannelID,
		"THREAD_TS":        reqCtx.ThreadTS,
		"MESSAGE_TS":       reqCtx.MessageTS,
		"SESSION_ID":       reqCtx.SessionID,
		"CHAT_TYPE":        reqCtx.ChatType,
		"CHAT_MODE_RULES":  buildChatModeRules(reqCtx),
		"TOOLS":            formatTools(tools),
		"USER_TIMEZONE":    time.Now().Location().String(),
		"RUNTIME":          buildRuntimeLine(reqCtx),
	}
	return renderTemplate(systemTemplate, data)
}

func buildChatModeRules(reqCtx *app.RequestContext) string {
	if reqCtx != nil && reqCtx.ChatType == "public" {
		trimmed := strings.TrimSpace(prompts.ChannelMode)
		if trimmed != "" {
			return trimmed
		}
		return "CHANNEL MODE (shared channel)"
	}
	trimmed := strings.TrimSpace(prompts.DMMode)
	if trimmed != "" {
		return trimmed
	}
	return "DM MODE (private)"
}

func renderTemplate(template string, values map[string]string) string {
	out := template
	for key, value := range values {
		token := "{{" + key + "}}"
		out = strings.ReplaceAll(out, token, value)
	}
	return out
}

func formatTools(tools []ToolSpec) string {
	if len(tools) == 0 {
		return "(no tools available)"
	}
	var out strings.Builder
	for _, tool := range tools {
		out.WriteString("- ")
		out.WriteString(tool.Name)
		out.WriteString(": ")
		out.WriteString(tool.Description)
		out.WriteString("\n")
	}
	return strings.TrimRight(out.String(), "\n")
}

func buildRuntimeLine(reqCtx *app.RequestContext) string {
	parts := []string{
		"agent=keycard-slack-agent",
		fmt.Sprintf("session=%s", reqCtx.SessionID),
		fmt.Sprintf("os=%s (%s)", runtime.GOOS, runtime.GOARCH),
	}
	return strings.Join(parts, " | ")
}
