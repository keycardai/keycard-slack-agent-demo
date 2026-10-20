# Keycard Slack agent

A Slack agent where the principal is chosen per request.

Most Slack agents hold one credential set for the whole channel. Anyone in the room gets the same reach, and the audit log records the bot rather than the person.

This one authenticates as itself, then exchanges the asking person's session for a short-lived credential scoped to them. It holds no standing access to anything it reads on your behalf. The write back to Slack is still an install-time bot token, which is the next thing to fix and is called out below.

It is a reference implementation, not a product. The interesting parts are small and marked in the code.

## The two properties worth reading the code for

**The tool list is per request.** For each MCP server the agent depends on, it tries to exchange the asking person's token. Success means the tool is offered. A refusal means it is not offered at all, so the model never learns it exists. Two people in the same channel get different capabilities from the same agent, with nothing configured per channel. See `serverForCandidate` in [`agent/runner_mcp.go`](agent/runner_mcp.go).

**A missing authorization goes to a DM.** When the exchange comes back needing consent, the agent sends the consent link privately rather than posting it in the channel. "Connect your Snowflake account" in a shared room tells everyone who lacks access, and it invites the wrong person to click it. See `sendReauthLink` in [`agent/reauth.go`](agent/reauth.go).

Two smaller guards follow the same instinct. A DM cannot be used to write into a channel it was not asked from, and the agent can only write in the conversation the request came from.

Both are read off the request, never off the model's tool call. That distinction is the whole point of them. An earlier version let the model waive the destination guard with an `allow_external` flag and name a DM recipient itself, which made both of them suggestions to the component they existed to constrain. If you take one idea from this repo, take that one: a guard the model can set is not a guard.

### One thing to know before you deploy it

The thread is the largest untrusted input in the system.

In a non-private thread the agent fetches up to 200 messages and prepends them to the user's message, so it answers with the conversation in view rather than only its own history. That is useful.

Everything in a shared channel was written by whoever is in the channel. So the prompt carries text the agent did not solicit, from people who are not the person asking. Treat it as hostile input.

What limits the damage is not the prompt. The credential the agent holds during that turn belongs to the person who asked and expires in minutes, so a successful injection reaches one person's access rather than the organization's. If you narrow one thing before running this in a real workspace, narrow this.

### The consent link is signed

The link that starts the OAuth flow carries one opaque signed token, and `/oauth/start` will not act on anything else.

It matters more than it looks. An earlier version took the target Slack user as a query parameter, so anyone who could reach the public base URL could craft a link naming somebody else. Send a colleague a link that legitimately came from the agent's own domain, they log in, and their Keycard access is now bound to your Slack account. The audit trail would name them for everything you then did.

The requested resources are inside the signature for the same reason. Appending a `resource` parameter would otherwise widen what the person is being asked to approve. Links expire after fifteen minutes.

If you take one thing from this repo for your own agent, take this and the guard note above. Both are cases where the thing that looked like a control was an input.

### Which resources become tools

The agent's own **dependency list** in Keycard, filtered to the entries that look MCP-facing, which currently means the identifier contains `mcp`.

Keycard defines an application's dependencies as its reach. Every destination it will exchange toward has to be on that list, and the list is what the consent screen enumerates, so it already answers "what may this agent reach." Reading anything else means guessing. The agent finds its own registration by looking for whichever application provides `KEYCARD_RESOURCE`, a pairing that has to exist for it to work at all.

The identifier check remains because a dependency list holds whatever the agent reaches, including plain APIs and vault secrets. Nothing in the inventory says which entries speak MCP. It is a much smaller job than it was: the worst it can do now is offer a tool for a non-MCP dependency, which fails at connect rather than reaching anything unintended.

**This replaces a rule that a live run proved wrong.** Discovery used to walk every application in the zone and scan the resources each one *provided*. A third-party API is registered as a resource provided by nobody, which is correct in Keycard's model. So a correctly configured agent whose dependency list named Linear's MCP endpoint got no tools, and nothing in the zone was wrong. It also meant the agent tried to exchange for MCP servers it had no dependency on, which could only ever fail.

If the tool list is empty, the log distinguishes the two causes. Either no application provides your anchor, which means the agent is misregistered and exchanges will fail too, or the anchor is fine and nothing on the dependency list looks like an MCP server.

## How a request flows

1. A message arrives over Socket Mode. The agent resolves the Slack user to a stored Keycard session, refreshing it if expired, and prompts for a link if there is none.
2. The zone inventory is read to find the agent's own dependency list, which is registration data only and never anyone's content. It uses the service account, because the Management API needs a service-account credential and a user's token is not one.
3. Each candidate is exchanged against the asking user's token. Ready servers become tools; ones needing consent become an authorize affordance.
4. The model runs with that tool list. Tool calls carry the user-scoped credential.
5. The reply posts back to the thread. Session history holds the user message and the assistant's text, so the agent's memory never exceeds what the channel already shows.

## What it does not do

Being explicit, because these are the parts people assume are handled.

- **It does not control disclosure.** The agent decides whether it may read on your behalf, not whether the answer is safe to show to everyone who can see the destination.
- **So an authorized read plus an authorized write can still be an unauthorized disclosure.** Nothing here prevents that.
- **The Slack write is not brokered.** Posting back uses the app's install-time bot token, the same for every user, while every read goes through a per-request exchange.
- **It does not detect a misleading request.** Scoping the credential limits what a fooled model reaches. It does not tell you whether the instruction was honest.

## Revocation

Three paths, because per-user binding means there is no single identity to switch off.

- **A person is deactivated.** `user_change` with `deleted` set drops that person's stored session and their prompt suppression.
- **The app is uninstalled, or its tokens are revoked.** Every stored session for that workspace goes.
- **A person's identity stops resolving.** If Keycard no longer returns a subject for a stored session, the session is deleted and they are asked to connect again.

This costs more than the model it replaces. One agent identity is a single thing to revoke. Per-user binding is a grant per person per resource, and revoking upstream access in Keycard is a separate act from what happens here. What you get for that is a compromise bounded to one person and an audit trail that names them.

## Running it

Requires Go 1.26.10 and a Keycard zone.

```bash
cp .env.example .env    # then fill it in
go run .
```

`.env` is read at startup when it exists, and real environment variables always win over it, so a deployment sets them properly and an image that happens to carry a `.env` cannot override them. Point `ENV_FILE` elsewhere if you keep it somewhere else.

That precedence has one sharp edge locally. If you ever run `set -a; source .env`, those values stay exported for the life of the shell and will shadow later edits to the file, so fixing the file appears to do nothing. You do not need to source anything; startup reports any key it found in the file but did not use for this reason.

**Slack app.** Create it from [`manifest.json`](manifest.json). At https://api.slack.com/apps choose *Create New App*, then *From an app manifest*, pick the workspace, and paste the file.

If you created the app before this manifest existed, check **App Home → Show Tabs → Messages Tab** and make sure *Allow users to send Slash commands and messages from the messages tab* is on. The tab defaults to read-only, which shows "Sending messages to this app has been turned off" and blocks the DM the consent flow depends on. The manifest sets it; the console default does not.

Two things the manifest cannot do for you. Generate an **app-level token** with `connections:write` under *Basic Information → App-Level Tokens*, which becomes `SLACK_APP_TOKEN`; Socket Mode needs it and it is not a bot scope. Then install the app and copy the bot token into `SLACK_BOT_TOKEN`.

The manifest asks for history scopes and message events on all four surfaces so the agent works wherever you put it. For a real deployment, drop the surfaces you are not using: an agent that only lives in public channels needs neither `groups:history` nor `mpim:history`.

Bot scopes, matched to the calls the agent actually makes:

- `chat:write` to post, edit, and delete
- `im:write` to open a DM, which the consent flow depends on
- `reactions:write` for the react action
- `users:read` to receive `user_change`, which is how a deactivation reaches the agent
- `channels:history`, `groups:history`, `im:history`, `mpim:history` for wherever you want it to work, covering both the message events and reading a thread for context

The subscriptions are message events rather than `app_mention`, because the agent decides for itself whether a message is addressed to it. There are no slash commands, so no `commands` scope.

`user_change`, `tokens_revoked`, and `app_uninstalled` are the revocation signals. Without them the agent keeps refresh tokens for people and workspaces it has no business holding, so they are not optional the way a surface is.

**Keycard zone.** Six things, in order. Steps 2, 4, and 5 are the ones that fail late and somewhere unrelated.

1. **An application for the agent**, with a client secret. Token exchange needs client authentication, so a public PKCE client will not work.

   For an app with a login flow the identifier convention is a URL matching the origin where the callback lives, so the same origin as `BASE_URL`.

2. **Register `<BASE_URL>/oauth/callback` as a redirect URI** on that application.

   The identifier being the origin does not imply the callback; that list is tracked separately. Missing it fails at the browser with `Unauthorized redirect URI`, after everything else has already worked. A localhost redirect also needs the app to be a native client, since web clients must use https and may not use localhost.

3. **Copy the credentials** from the application's Application Credentials tab into `KEYCARD_CLIENT_ID` and `KEYCARD_CLIENT_SECRET`.

   These are generated by Keycard and are **not** the identifier you chose in step 1. Confusing the two fails at the first token exchange, which makes it look like a policy problem.

4. **A small resource the agent provides**, such as `urn:keycard:slack-agent`, Zone Provider. Set the agent's application as its **provider**, and add it to that application's **dependencies** as well. Put it in `KEYCARD_RESOURCE`.

   Both halves matter. An application may only exchange tokens addressed to a resource it provides, so without the pairing every per-request exchange returns `invalid_grant`. The resource never serves traffic; its only job is to give the login token a legal audience to exchange from.

5. **A service account**, under Organization in the console, into `KEYCARD_SERVICE_ACCOUNT_ID` and `KEYCARD_SERVICE_ACCOUNT_SECRET`.

   Required rather than optional. Reading the zone inventory goes through the Management API, which wants a service-account credential, and a user's access token is audienced to a resource so it is not one. Without this the agent runs and answers, and simply never finds a tool.

6. **The MCP servers the agent should reach**, on the application's dependency list.

   Add them before anyone links their account: consent is recorded at login from the list as it stood then, and a dependency added later makes every affected person consent again.

**Environment.** `ENCRYPTION_KEY` must be exactly 32 bytes and encrypts stored user tokens at rest. `STATE_SECRET` signs the OAuth state parameter. `BASE_URL` has to be reachable by a browser, because the consent redirect lands there. See [`.env.example`](.env.example) for the full list.

## What a working first run looks like

In order, so you can tell how far you got:

1. `go run .` prints `http server listening` and stays running. The bot shows as online in Slack.
2. You DM it. It replies asking you to connect, with a link to `<BASE_URL>/oauth/start?t=…`.
3. You click through the Keycard consent screen and land back on a success page.
4. You DM it again. The log shows `mcp discovery resolved`, then `mcp server status … status=ok tools=N`, then `prompt tools:` listing those tools by name.
5. You ask it something that needs one, and it answers from real data.

Step 4 is the line worth watching. `prompt tools:` is what the model was actually told it has. If a server reached `status=ok` and its tools are not on that line, the model cannot use them, however healthy the rest of the log looks.

Reaching step 5 means the whole path works: the signed consent link, the callback, encrypted token storage, a token exchange against your anchor, a second exchange for the MCP server, and the MCP handshake.

**Expect the first message of a session to take a few seconds.** Discovery, two exchanges, and the MCP handshake all happen inline before the model is called. Later messages in the same session use the caches. Earlier versions did that work in the background and answered immediately with no tools, which was faster and useless.

## One lesson that is not about the code

The agent has to **call** its tools rather than reason about whether it has access.

An earlier system prompt said to offer an authorization link "rather than guessing or trying another route." Holding 62 working Linear tools, the model followed that instruction exactly.

It decided in advance that it lacked access, called the authorize tool as a way of checking, and read its empty response as confirmation. Five times it told the person their administrator needed to fix the agent config. Then it tried Linear directly and it worked first time.

So [`prompts/system.md`](prompts/system.md) now tells it to try, and says the authorize tool is not a way to find out what it can reach. If you adapt this agent, keep that. A model given a tool and a reason to doubt itself will produce a confident, detailed, wrong diagnosis.

## When it does not work

Every one of these came out of a real first run, and each fails somewhere unrelated to its cause.

| What you see | What it is |
|---|---|
| `missing required env vars:` everything, with `no .env found` | You are not in the repo root, or the file is named differently. Set `ENV_FILE`. |
| `missing required env vars:` everything, and `.env` is right there | Older builds did not read `.env` at all. Pull the current version. |
| `these still hold placeholder values` | Example text is still in `.env`. Leave a value empty rather than leaving the example text. |
| `placeholder values … (from the environment, shadowing .env)` | A previous `set -a; source .env` exported the old values into this shell, and they win. Open a new shell. |
| `cipher error: encryption key must be 32 bytes` | `ENCRYPTION_KEY` is the wrong length. `openssl rand -hex 16` gives 32 characters. |
| `unable to open database file` | `DATABASE_PATH` points somewhere that cannot be created, usually the deployed `/data` path on a laptop. |
| Slack says *Sending messages to this app has been turned off* | App Home, Messages Tab, read-only. See the Slack app section. |
| `Unauthorized redirect URI` in the browser | `<BASE_URL>/oauth/callback` is not registered on the Keycard application. |
| `invalid_grant: Client is not allowed to exchange token for this resource` | The agent's application does not **provide** `KEYCARD_RESOURCE`. |
| The agent works but finds no tools | Nothing on the agent's dependency list looks MCP-facing. The log says so. |
| Tools resolve in the log but the agent says it has none | Older builds raced a background warm at two layers, and built the prompt's tool list before resolving anything, so the prompt disagreed with the tools the model was handed. Pull the current version. |
| `list inventory: … needs KEYCARD_SERVICE_ACCOUNT_ID` | Discovery has no service account. It is required, not optional: the Management API will not accept a user's token. |
| `keycard api: status 401: Authentication required` | An older build sent a user's token to the Management API. Pull the current version and set a service account. |
| `mcp discovery empty: no_application_provides_anchor` | Nothing provides `KEYCARD_RESOURCE`, so the agent cannot be found in its own zone and exchanges will fail too. |
| `interaction_required` for a resource you can reach | A dependency was added after you linked. Consent is recorded at login, so link again. |

## Tests

```bash
go test ./...                  # 193 tests, no network, no API key
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
