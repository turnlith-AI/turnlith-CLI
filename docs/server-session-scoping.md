# Proposal: conversation-scoped sessions (server)

**Status:** proposal — nothing here is implemented. It needs changes in the
turnlith server (`agenticInference`), which this repository cannot make alone.
The references below are to that repository as of this writing.

## The problem

The server gives every agent exactly one conversation:

- `agents.session_id` links an agent to a single backing session
  (`src/db/repos/agents.ts`); `resolveAgentSession`
  (`src/modules/facade/agentSession.ts`) creates it on first contact and returns
  it for every later request — "one long-lived session per agent".
- The per-task frozen context (`sessions.frozen_task`, `src/modules/sessions/task.ts`)
  is stored on that one session row, keyed by `anchorSeq`, the sequence number of
  the user message that opened the task.

Consequences for `tlc`:

1. **Projects bleed into each other.** Running `tlc` in `~/code/api` and then in
   `~/code/website` with the same agent continues *one* conversation: the rolling
   summary, recent turns and retrieval all mix the two codebases.
2. **Two terminals corrupt each other.** Two `tlc` processes on one agent append to
   the same history. A continuation whose session's newest user message is not its
   own anchor is (correctly) refused the freeze, but both clients' tool rows still
   interleave in one append-only history and each sees the other's prompts.
3. **There is no "new conversation".** `/clear` only drops the client's local task
   history; the server session, its summary and its window persist forever.

None of this is fixable in the client: the client does not choose the session.

## Proposal

### 1. A conversation key on chat requests

The facade accepts an optional header:

```
x-tlith-conversation: <opaque string, ≤ 128 chars, [A-Za-z0-9._:-]>
```

(A header rather than the OpenAI `user` field: `user` means "end user" to other
clients and is currently ignored by the request schema; a header cannot collide
with existing bodies.)

- Absent → today's behavior exactly (the agent's own session). Every existing
  client keeps working.
- Present → the facade resolves the session for `(agent, key)`, creating it on first
  use with the same settings `resolveAgentSession` applies today (model, system
  prompt, embedding pin, context budget, reasoning effort, `createdBy`).

### 2. Storage

A small table, tenant-scoped under the same RLS policy as `sessions`:

```
agent_conversations(
  agent_id uuid references agents on delete cascade,
  key      text,
  session_id uuid references sessions on delete cascade,
  created_at timestamptz default now(),
  last_used_at timestamptz,
  primary key (agent_id, key)
)
```

`resolveAgentSession(clients, orgId, agent, key?)` looks up / inserts there.
Concurrent first contact is handled with `insert … on conflict do nothing` + re-select
(the current race merely orphans a session; this one need not).

### 3. Memory semantics — what stays shared

- **Durable agent memory stays per agent.** Facts distilled from any of the agent's
  conversations are retrieved by all of them, which is the point: a new conversation
  starts fresh but the agent still knows the user's durable preferences.
- **Rolling summary, recent window and session-scoped retrieval are per
  conversation.** `search_memory` with `scope: session` therefore searches the
  conversation it is called from; add `scope: agent` to search every conversation of
  the agent.
- **Deletion:** `memories` rows cascade from their *source session*
  (`consolidation.ts`, so GDPR erasure carries facts away). That must keep
  working. The client's "new conversation" must therefore *stop using* a key, not
  delete the old session; retention stays with the existing purge job.

### 4. Concurrency

With keys, two terminals in the same project still share a conversation if they
use the same key. Make that explicit instead of corrupting it: while a task is in
flight on a session (between a fresh prompt and its final non-tool turn), a second
fresh prompt for the same session returns **409 `conversation_busy`** with the
task's age. `tlc` shows "another tlc is working in this conversation" and offers
`/new`. (Open question: define "in flight" by a lease with a TTL, e.g. 15 minutes
without a request, so a crashed client cannot wedge a conversation.)

### 5. Small related changes

- **Heartbeats.** `lib/sse.ts` sends no comment frames, so a reasoning model that
  thinks silently for minutes looks like a dead connection to a client idle timeout.
  Emit `: ping` every ~15 s while a turn is open; `tlc` can then tighten its default
  5-minute idle limit.
- **Cost in the usage chunk.** The final chunk carries `prompt_tokens` /
  `completion_tokens`; adding `cost_micros` (already computed for `usage_events`)
  lets the CLI show spend per prompt. `/v1/me/usage` is web-session only today.
- **Listing:** `GET /v1/agents/:id/conversations` (key, created, last used, message
  count) so `/resume` can show conversations that started on another machine.

## Client work once this lands

- Send `x-tlith-conversation: <hash of canonical project root>` by default, so each
  project has its own conversation per agent.
- `/new` starts another: `hash(root) + "-" + n`, with `n` kept in
  `~/.config/tlith/tlc.json` (per project); `/clear` keeps meaning "drop the local
  task history".
- Surface `409 conversation_busy` in `friendlyErr`; add the conversation key and age
  to `/status` and `tlc doctor`.
- Detect support: servers without it ignore the header, so `tlc doctor` can compare
  `/v1/models` capability flags (add `"conversations": true`) rather than guess.

## Rollout

Additive and backwards compatible: the header is optional, the table is new, the
old `agents.session_id` path stays as the default conversation. Migrate nothing.
