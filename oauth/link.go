package oauth

import (
	"errors"
	"strings"
	"time"
)

// LinkTTL is how long a consent link stays usable.
const LinkTTL = 15 * time.Minute

// ErrLinkInvalid means a consent link was missing, tampered with, or expired.
var ErrLinkInvalid = errors.New("invalid consent link")

// LinkRequest is who a consent link is for, and what it asks consent for.
type LinkRequest struct {
	TeamID    string
	UserID    string
	ChannelID string
	Resources []string
}

// LinkSigner mints the signed part of a consent URL. The agent holds one of these so a link can
// only be created by the agent itself.
type LinkSigner interface {
	SignLink(req LinkRequest) (string, error)
}

// SignLink produces the opaque token that carries a consent request.
//
// This exists because /oauth/start used to read the target Slack user straight out of the query
// string. Anyone who could reach the public base URL could craft a link naming somebody else, and
// the callback would store the Keycard tokens of whoever clicked it against that Slack id. Sending
// a colleague a link that legitimately came from the agent's own domain was enough to have their
// access bound to your Slack account, which inverts the one property this agent exists to provide.
//
// So the whole consent request is signed: who it is for, where it came from, and which resources it
// asks for. The requested resources matter as much as the user, since an appended resource
// parameter would otherwise widen what the person is being asked to approve.
func (s *StateSigner) SignLink(req LinkRequest) (string, error) {
	team := strings.TrimSpace(req.TeamID)
	user := strings.TrimSpace(req.UserID)
	if team == "" || user == "" {
		return "", errors.New("team and user are required to sign a consent link")
	}
	encoded, err := encodeStateResources(req.Resources)
	if err != nil {
		return "", err
	}
	return s.WithClock(s.now, LinkTTL).Sign(map[string]string{
		"team":            team,
		"user":            user,
		"channel":         strings.TrimSpace(req.ChannelID),
		stateKeyResources: encoded,
	})
}

// VerifyLink recovers a consent request from a signed token.
func (s *StateSigner) VerifyLink(token string) (LinkRequest, error) {
	if strings.TrimSpace(token) == "" {
		return LinkRequest{}, ErrLinkInvalid
	}
	claims, err := s.Verify(token)
	if err != nil {
		return LinkRequest{}, errors.Join(ErrLinkInvalid, err)
	}
	resources, err := decodeStateResources(claims)
	if err != nil {
		return LinkRequest{}, errors.Join(ErrLinkInvalid, err)
	}
	req := LinkRequest{
		TeamID:    claims["team"],
		UserID:    claims["user"],
		ChannelID: claims["channel"],
		Resources: resources,
	}
	if req.TeamID == "" || req.UserID == "" {
		return LinkRequest{}, ErrLinkInvalid
	}
	return req, nil
}
