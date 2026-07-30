package app

import "fmt"

type RequestContext struct {
	KeycardIdentity string
	BaseURL         string
	TeamID          string
	UserID          string
	ChannelID       string
	ThreadTS        string
	MessageTS       string
	SessionID       string
	ChatType        string
	IsPrivate       bool
}

type StoredMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func MakeSessionID(teamID, channelID, threadTS string) string {
	if threadTS != "" {
		return fmt.Sprintf("slack:%s:%s:%s", teamID, channelID, threadTS)
	}
	return fmt.Sprintf("slack:%s:%s", teamID, channelID)
}
