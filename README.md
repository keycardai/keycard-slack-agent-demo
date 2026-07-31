# Keycard Slack agent

A Slack agent where the principal is chosen per request.

Most Slack agents hold one credential set for the whole channel. Anyone in the room gets the same reach, and the audit log records the bot rather than the person. This one authenticates as itself and then, when a person is in the loop, exchanges that person's session for a short-lived credential scoped to them. The agent holds no standing access to anything downstream.

It is a reference implementation, not a product. The interesting parts are small and marked in the code.

## The two properties worth reading the code for

**The tool list is per request.** For each resource in the zone, the agent tries to exchange the asking user's token. Success means the tool is offered. A refusal means the tool is not offered at all, so the model never learns it exists. Two people in the same channel get different capabilities from the same agent, with nothing configured per channel. See `serverForCandidate` in [`agent/runner_mcp.go`](agent/runner_mcp.go).

**A missing authorization goes to a DM.** When the exchange comes back needing consent, the agent sends the consent link privately rather than posting it in the channel. "Connect your Snowflake account" in a shared room tells everyone who lacks access, and it invites the wrong person to click it. See `sendReauthLink` in [`agent/reauth.go`](agent/reauth.go).

Two smaller guards follow the same instinct. A DM cannot be used to write into a channel it was not asked from, and posting anywhere other than where the question arrived takes an explicit flag.

### One thing to know before you deploy it

The thread is the largest untrusted input in the system.

In a non-private thread the agent fetches up to 200 messages and prepends them to the user's message, so it answers with the conversation in view rather than only its own history. That is useful.

Everything in a shared channel was written by whoever is in the channel. So the prompt carries text the agent did not solicit, from people who are not the person asking. Treat it as hostile input.

What limits the damage is not the prompt. The credential the agent holds during that turn belongs to the person who asked and expires in minutes, so a successful injection reaches one person's access rather than the organization's. If you narrow one thing before running this in a real workspace, narrow this.

### Which resources become tools

Two conditions have to hold. The application declares the `mcp-provider` trait, and the resource identifier looks MCP-facing, which currently means it contains `mcp`.

The second condition stands in for a field that does not exist. An MCP-provider application also provides ordinary resources: a github app provides `github-api` alongside `mcp-github`, and only the latter is a tool. Nothing in the inventory says which resource is the MCP endpoint.

Requiring the trait as well is what stops a resource named `mcpayments` being offered by an application that never claimed to serve MCP. An application declaring no traits falls back to the identifier alone, for zones registered before traits existed.

Worth knowing because it decides what the agent can reach, and it is the piece most likely to change.

## How a request flows

1. A message arrives over Socket Mode. The agent resolves the Slack user to a stored Keycard session, refreshing it if expired, and prompts for a link if there is none.
2. The zone inventory is read with a service account to find candidate MCP servers. This reads registration data only, never a user's data.
3. Each candidate is exchanged against the asking user's token. Ready servers become tools; ones needing consent become an authorize affordance.
4. The model runs with that tool list. Tool calls carry the user-scoped credential.
5. The reply posts back to the thread. Session history holds the user message and the assistant's text, so the agent's memory never exceeds what the channel already shows.

## What it does not do

Being explicit, because these are the parts people assume are handled.

- **It does not control disclosure.** The agent decides whether it may read on your behalf, not whether the answer is safe to show to everyone who can see the destination.
- **So an authorized read plus an authorized write can still be an unauthorized disclosure.** Nothing here prevents that.
- **The Slack write is not brokered.** Posting back uses the app's install-time bot token, the same for every user, while every read goes through a per-request exchange.
- **It does not detect a misleading request.** Scoping the credential limits what a fooled model reaches. It does not tell you whether the instruction was honest.

## Running it

Requires Go 1.24 and a Keycard zone.

```bash
cp .env.example .env    # then fill it in
go run .
```

**Slack app.** Enable Socket Mode and generate an app-level token (`SLACK_APP_TOKEN`).

Bot scopes, matched to the calls the agent actually makes:

- `chat:write` to post, edit, and delete
- `im:write` to open a DM, which the consent flow depends on
- `reactions:write` for the react action
- `channels:history`, `groups:history`, `im:history`, `mpim:history` for wherever you want it to work, covering both the message events and reading a thread for context

Event subscriptions are message events rather than `app_mention`, because the agent decides for itself whether a message is addressed to it. Subscribe to `message.im` plus whichever of `message.channels`, `message.groups`, and `message.mpim` you need. No slash commands, so no `commands` scope.

**Keycard zone.** Register the agent as an application with a client secret, and give it a small resource of its own that it provides.

That pairing is not optional. An application may only exchange tokens addressed to a resource it provides, so without it every per-request exchange is rejected. Set that resource as `KEYCARD_RESOURCE`, and add the MCP servers the agent should reach to its dependency list.

**Environment.** `ENCRYPTION_KEY` must be exactly 32 bytes and encrypts stored user tokens at rest. `STATE_SECRET` signs the OAuth state parameter. `BASE_URL` has to be reachable by a browser, because the consent redirect lands there. See [`.env.example`](.env.example) for the full list.

## Tests

```bash
go test ./...                  # 155 tests, no network, no API key
go vet ./...
```

Golden tests exercise the agent loop against a live model, with Slack faked and a temporary database. They are behind a build tag so they never run by accident:

```bash
export ANTHROPIC_API_KEY=...
go test -tags golden -run TestGolden -v -timeout 120s ./agent/
```

[`internal/fakezone`](internal/fakezone) is an in-process Keycard zone: OAuth discovery, an RFC 8693 token endpoint, and the Management API list endpoints. It drives the real SDK rather than a stubbed interface, which is the point. This repo spent four and a half months not compiling against the published SDK and the test suite did not notice, because nothing exercised the SDK.

Those tests assert the things the agent rests on. The exchange subject is the asking user's token while the agent authenticates as itself. A missing consent stays distinguishable from a denial. An unconfigured resource fails closed, and the inventory read never presents a user's token.

What is still not covered is a real workspace. MCP servers are not wired into the golden tests, and no test speaks to a live zone, so treat a green suite as evidence against regression rather than proof it runs.

## Deploying

`Dockerfile` builds a static-ish image with CGO enabled for SQLite. `fly.toml` is a starting point rather than a recommendation, and the volume mount matters because the database holds the per-user sessions.

## License

MIT. See [LICENSE](LICENSE).
