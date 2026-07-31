package agent

import (
	"context"
	"log"
	"strings"

	"github.com/keycardai/keycard-slack-agent-demo/app"
)

func (r *Runner) sendReauthLink(ctx context.Context, reqCtx *app.RequestContext, resources []string, destination, channel, threadTS string) ToolResult {
	if r.SlackActions == nil {
		return ToolResult{Content: "slack actions not configured", IsError: true}
	}
	resources = uniqueResources(resources)
	if len(resources) == 0 {
		return ToolResult{Content: "missing resource", IsError: true}
	}
	if r.LinkSigner == nil {
		log.Printf("consent link unavailable: session=%s reason=no_signer", reqCtx.SessionID)
		return ToolResult{Content: "consent links are not configured", IsError: true}
	}
	signed, err := r.LinkSigner.SignLink(reqCtx.TeamID, reqCtx.UserID, reqCtx.ChannelID, resources)
	if err != nil {
		log.Printf("consent link signing failed: session=%s err=%v", reqCtx.SessionID, err)
		return ToolResult{Content: "failed to build consent link", IsError: true}
	}
	link := buildConsentURL(r.BaseURL, signed)
	if link == "" {
		return ToolResult{Content: "failed to build consent link", IsError: true}
	}
	if strings.TrimSpace(destination) == "" {
		destination = "auto"
	}
	if destination == "dm" || (destination == "auto" && !reqCtx.IsPrivate) {
		dmID, err := r.SlackActions.OpenDM(ctx, reqCtx.UserID)
		if err != nil || dmID == "" {
			log.Printf("reauth dm failed: session=%s err=%v", reqCtx.SessionID, err)
			return ToolResult{Content: "failed to open dm", IsError: true}
		}
		if _, err := r.SlackActions.PostMessage(dmID, link, ""); err != nil {
			log.Printf("reauth dm failed: session=%s err=%v", reqCtx.SessionID, err)
			return ToolResult{Content: "failed to send dm", IsError: true}
		}
		return ToolResult{Content: "ok"}
	}
	if channel == "" {
		channel = reqCtx.ChannelID
	}
	if channel != reqCtx.ChannelID {
		return ToolResult{Content: "can only post in the channel the request came from", IsError: true}
	}
	if threadTS == "" {
		threadTS = reqCtx.ThreadTS
	}
	if _, err := r.SlackActions.PostMessage(channel, link, threadTS); err != nil {
		log.Printf("reauth send failed: session=%s err=%v", reqCtx.SessionID, err)
		return ToolResult{Content: "failed to send reauth link", IsError: true}
	}
	return ToolResult{Content: "ok"}
}
