# Golden File Tests

Golden file tests validate the agent's structural behavior against a live LLM. Everything except the LLM is faked: Slack actions are a no-op capture and the database is a temp SQLite file. This lets us assert on *what the agent does*, meaning which tools it calls, how many turns it takes, and how it routes responses, without being brittle to exact LLM wording.

## Running

```bash
export ANTHROPIC_API_KEY=sk-ant-...
go test -tags golden -run TestGolden -v -timeout 120s ./agent/
```

Tests skip gracefully when `ANTHROPIC_API_KEY` is not set for scenarios with `"needs_llm": true`.

Golden tests never run in normal `go test`, they require the `golden` build tag.

## Scenario Schema

Each `.json` file defines one test scenario:

```jsonc
{
  "name": "dm_simple_response",
  "description": "...",
  "needs_llm": true,
  "skip": "",
  "context": {
    "keycard_identity": "test-user",
    "team_id": "T001",
    "user_id": "U001",
    "channel_id": "D001",
    "thread_ts": "",
    "message_ts": "100.001",
    "chat_type": "private",
    "is_private": true
  },
  "user_message": "Say hello",
  "setup": {
    "keycard_token": "test-token"
  },
  "expect": {
    "completed": true,
    "no_errors": true,
    "max_turns": 10,
    "tools_called": ["slack_action"],
    "tools_not_called": ["authorize_resource"],
    "text_contains": ["hello"],
    "text_contains_any": ["hi", "hello"],
    "text_not_contains": ["error"],
    "has_tool_call_count": 1
  }
}
```

All `expect` fields are optional. Omitted fields are not checked.

## What Gets Faked

| Component | Real / Fake | Implementation |
|-----------|-------------|----------------|
| Anthropic LLM | Real | Live API calls via `anthropic.Client` |
| Slack actions | Fake | `fakeSlackActions` in `agent/golden_test.go`, no-op with message capture |
| Database | Real (temp) | Fresh SQLite in `t.TempDir()`, destroyed after each test |
| MCP servers | None | Not wired |

## How Assertions Work

**Tool calls** are checked from the LLM's tool call events (`EventToolCall` with `ToolName`).

**Text assertions** operate on the concatenated `EventText` content from the agent's response. They're case-insensitive to handle LLM variation.

**Turns** count `EventTurnStart` events. `max_turns` prevents runaway loops.

## Writing New Scenarios

1. Create a new `.json` file in this directory
2. Set `context` to simulate the Slack event (DM vs channel, thread vs root)
3. Write a `user_message` that should deterministically trigger specific tools
4. Set `expect` to assert on structural behavior, not exact text

Tips:
- Keep prompts directive to reduce LLM variability
- Use `text_contains_any` for answers that could be phrased multiple ways
- Use `tools_not_called` to verify routing boundaries
- Set `max_turns` low for simple scenarios to catch infinite loops early
- `"needs_llm": false` scenarios run without an API key

## File Inventory

| File | Tests |
|------|-------|
| `dm_simple_response.json` | DM → pure text, no tools |
| `dm_simple_no_coding.json` | DM → simple response, no tools |
| `channel_mention_triggers.json` | Public channel → agent responds |
