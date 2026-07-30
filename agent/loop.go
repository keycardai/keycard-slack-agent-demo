package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/keycardai/slack-agent/app"
)

type loopContext struct {
	systemPrompt string
	messages     []anthropic.MessageParam
	tools        []anthropic.ToolUnionParam
	toolSpecs    []ToolSpec
}

type toolCall struct {
	ID    string
	Name  string
	Input map[string]any
}

func (r *Runner) runLoop(ctx context.Context, reqCtx *app.RequestContext, loopCtx *loopContext, keycardToken string, mcpTools map[string]MCPToolRef, authTools map[string]AuthToolRef, assistantText *strings.Builder, events chan<- AgentEvent) error {
	for iteration := 0; iteration < MaxAgentIterations; iteration++ {
		if iteration > 0 {
			events <- AgentEvent{Type: EventTurnStart}
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := r.callAnthropicWithRetry(ctx, loopCtx.systemPrompt, loopCtx.messages, loopCtx.tools)
		if err != nil {
			return err
		}
		logAnthropicUsage(reqCtx, resp)

		toolCalls := handleAssistantResponse(resp, assistantText, events)
		logToolCalls(reqCtx, toolCalls)
		if len(toolCalls) == 0 {
			events <- AgentEvent{Type: EventTurnEnd}
			return nil
		}

		toolResults := r.executeToolCalls(ctx, reqCtx, toolCalls, loopCtx, keycardToken, mcpTools, authTools, events)
		loopCtx.messages = append(loopCtx.messages, assistantMessageParam(resp))
		loopCtx.messages = append(loopCtx.messages, anthropic.MessageParam{
			Role:    anthropic.MessageParamRoleUser,
			Content: toolResults,
		})
		events <- AgentEvent{Type: EventTurnEnd}
	}

	return fmt.Errorf("max iterations exceeded")
}

func handleAssistantResponse(resp *anthropic.Message, assistantText *strings.Builder, events chan<- AgentEvent) []toolCall {
	events <- AgentEvent{Type: EventMessageStart, MessageRole: "assistant"}
	defer func() {
		events <- AgentEvent{Type: EventMessageEnd, MessageRole: "assistant"}
	}()

	toolCalls := make([]toolCall, 0)
	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			events <- AgentEvent{Type: EventText, Content: block.Text}
			assistantText.WriteString(block.Text)
		case "tool_use":
			events <- AgentEvent{Type: EventToolCall, Content: block.Name, ToolName: block.Name, ToolCallID: block.ID}
			toolCalls = append(toolCalls, toolCall{
				ID:    block.ID,
				Name:  block.Name,
				Input: parseToolInput(block.Input),
			})
		}
	}

	return toolCalls
}

func logToolCalls(reqCtx *app.RequestContext, calls []toolCall) {
	if reqCtx == nil || len(calls) == 0 {
		return
	}
	for _, call := range calls {
		log.Printf(
			"agent tool call: session=%s tool=%s id=%s input=%s",
			reqCtx.SessionID,
			call.Name,
			call.ID,
			summarizeToolInput(call.Input),
		)
	}
}

func logAnthropicUsage(reqCtx *app.RequestContext, resp *anthropic.Message) {
	if resp == nil || reqCtx == nil {
		return
	}
	usage := resp.Usage
	log.Printf(
		"anthropic usage: session=%s model=%s stop_reason=%s input_tokens=%d output_tokens=%d cache_read_input_tokens=%d cache_creation_input_tokens=%d web_search_requests=%d service_tier=%s",
		reqCtx.SessionID,
		string(resp.Model),
		string(resp.StopReason),
		usage.InputTokens,
		usage.OutputTokens,
		usage.CacheReadInputTokens,
		usage.CacheCreationInputTokens,
		usage.ServerToolUse.WebSearchRequests,
		string(usage.ServiceTier),
	)
}

func (r *Runner) executeToolCalls(ctx context.Context, reqCtx *app.RequestContext, toolCalls []toolCall, loopCtx *loopContext, keycardToken string, mcpTools map[string]MCPToolRef, authTools map[string]AuthToolRef, events chan<- AgentEvent) []anthropic.ContentBlockParamUnion {
	results := make([]anthropic.ContentBlockParamUnion, 0, len(toolCalls))

	for _, call := range toolCalls {
		log.Printf("agent tool start: session=%s tool=%s id=%s input=%s", reqCtx.SessionID, call.Name, call.ID, summarizeToolInput(call.Input))
		events <- AgentEvent{Type: EventToolExecutionStart, ToolName: call.Name, ToolCallID: call.ID}
		input := call.Input
		spec, found := findToolSpec(loopCtx.toolSpecs, call.Name)
		if found {
			validated, err := validateToolArguments(spec, input)
			if err != nil {
				result := ToolResult{Content: err.Error(), IsError: true}
				log.Printf("agent tool end: session=%s tool=%s id=%s error=%t output_len=%d", reqCtx.SessionID, call.Name, call.ID, result.IsError, len(result.Content))
				events <- AgentEvent{Type: EventToolExecutionEnd, ToolName: call.Name, ToolCallID: call.ID, Content: result.Content, IsError: result.IsError}
				events <- AgentEvent{Type: EventToolResult, Content: result.Content, ToolName: call.Name, ToolCallID: call.ID, IsError: result.IsError}
				results = append(results, anthropic.NewToolResultBlock(call.ID, result.Content, true))
				emitToolResultMessage(events, call, result)
				continue
			}
			input = validated
		} else if _, ok := mcpTools[call.Name]; !ok {
			if _, ok := authTools[call.Name]; !ok {
				result := ToolResult{Content: fmt.Sprintf("tool %q not found", call.Name), IsError: true}
				log.Printf("agent tool end: session=%s tool=%s id=%s error=%t output_len=%d", reqCtx.SessionID, call.Name, call.ID, result.IsError, len(result.Content))
				events <- AgentEvent{Type: EventToolExecutionEnd, ToolName: call.Name, ToolCallID: call.ID, Content: result.Content, IsError: result.IsError}
				events <- AgentEvent{Type: EventToolResult, Content: result.Content, ToolName: call.Name, ToolCallID: call.ID, IsError: result.IsError}
				results = append(results, anthropic.NewToolResultBlock(call.ID, result.Content, true))
				emitToolResultMessage(events, call, result)
				continue
			}
		}

		result := r.executeTool(ctx, reqCtx, keycardToken, call.Name, input, mcpTools, authTools)
		log.Printf("agent tool end: session=%s tool=%s id=%s error=%t output_len=%d", reqCtx.SessionID, call.Name, call.ID, result.IsError, len(result.Content))
		events <- AgentEvent{Type: EventToolExecutionEnd, ToolName: call.Name, ToolCallID: call.ID, Content: result.Content, IsError: result.IsError}
		events <- AgentEvent{Type: EventToolResult, Content: result.Content, ToolName: call.Name, ToolCallID: call.ID, IsError: result.IsError}
		results = append(results, anthropic.NewToolResultBlock(call.ID, result.Content, result.IsError))
		emitToolResultMessage(events, call, result)
		r.postToolOutput(reqCtx, call, result)
	}

	return results
}

func summarizeToolInput(input map[string]any) string {
	if len(input) == 0 {
		return "{}"
	}
	keys := make([]string, 0, len(input))
	for key := range input {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		value := input[key]
		switch v := value.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("%s=len(%d)", key, len(v)))
		case []any:
			parts = append(parts, fmt.Sprintf("%s=len(%d)", key, len(v)))
		case map[string]any:
			parts = append(parts, fmt.Sprintf("%s=map", key))
		case bool:
			parts = append(parts, fmt.Sprintf("%s=%t", key, v))
		case int, int64, float64:
			parts = append(parts, fmt.Sprintf("%s=%v", key, v))
		default:
			if value == nil {
				parts = append(parts, fmt.Sprintf("%s=nil", key))
			} else {
				parts = append(parts, fmt.Sprintf("%s=%T", key, value))
			}
		}
	}
	return strings.Join(parts, " ")
}

func emitToolResultMessage(events chan<- AgentEvent, call toolCall, result ToolResult) {
	events <- AgentEvent{Type: EventMessageStart, MessageRole: "toolResult", ToolName: call.Name, ToolCallID: call.ID, Content: result.Content}
	events <- AgentEvent{Type: EventMessageEnd, MessageRole: "toolResult", ToolName: call.Name, ToolCallID: call.ID, Content: result.Content, IsError: result.IsError}
}

func assistantMessageParam(resp *anthropic.Message) anthropic.MessageParam {
	assistantBlocks := make([]anthropic.ContentBlockParamUnion, 0, len(resp.Content))
	for _, block := range resp.Content {
		assistantBlocks = append(assistantBlocks, block.ToParam())
	}
	return anthropic.MessageParam{
		Role:    anthropic.MessageParamRoleAssistant,
		Content: assistantBlocks,
	}
}

func parseToolInput(input json.RawMessage) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	decoded := map[string]any{}
	if err := json.Unmarshal(input, &decoded); err != nil {
		return map[string]any{"raw": string(input)}
	}
	return decoded
}
