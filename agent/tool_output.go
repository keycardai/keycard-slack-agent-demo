package agent

import (
	"fmt"
	"log"
	"strings"

	"github.com/keycardai/keycard-slack-agent-demo/app"
)

const toolOutputChunkSize = 5000

func (r *Runner) postToolOutput(reqCtx *app.RequestContext, call toolCall, result ToolResult) {
	if r.SlackActions == nil {
		return
	}
	if call.Name == ToolSlackAction {
		return
	}
	if !shouldPostToolOutput(call.Input) {
		return
	}
	output := strings.TrimSpace(result.Content)
	if output == "" {
		return
	}
	threadTS := reqCtx.ThreadTS
	if threadTS == "" {
		threadTS = reqCtx.MessageTS
	}
	if threadTS == "" {
		log.Printf("tool output skipped: session=%s tool=%s missing thread_ts", reqCtx.SessionID, call.Name)
		return
	}

	chunks := splitSlackChunks(output, toolOutputChunkSize)
	prefix := fmt.Sprintf("Tool output (%s):", call.Name)
	if result.IsError {
		prefix = fmt.Sprintf("Tool output (%s) [error]:", call.Name)
	}
	for i, chunk := range chunks {
		header := prefix
		if len(chunks) > 1 {
			header = fmt.Sprintf("%s (%d/%d)", prefix, i+1, len(chunks))
		}
		text := fmt.Sprintf("%s\n```%s```", header, chunk)
		ts, err := r.SlackActions.PostMessage(reqCtx.ChannelID, text, threadTS)
		if err != nil {
			log.Printf("tool output post failed: session=%s tool=%s err=%v", reqCtx.SessionID, call.Name, err)
			return
		}
		log.Printf("tool output post ok: session=%s tool=%s ts=%s", reqCtx.SessionID, call.Name, ts)
	}
}

func shouldPostToolOutput(input map[string]any) bool {
	if input == nil {
		return false
	}
	value, ok := input["post_output"]
	if !ok {
		return false
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	case float64:
		return typed != 0
	case int:
		return typed != 0
	case int64:
		return typed != 0
	default:
		return false
	}
}

func splitSlackChunks(value string, max int) []string {
	if max <= 0 || len(value) <= max {
		return []string{value}
	}
	chunks := make([]string, 0, (len(value)/max)+1)
	for len(value) > 0 {
		if len(value) <= max {
			chunks = append(chunks, value)
			break
		}
		chunks = append(chunks, value[:max])
		value = value[max:]
	}
	return chunks
}
