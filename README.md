# GoClaw

GoClaw is a Go-based chat runtime built around person-centered isolation.

## Docs entry

- Chinese architecture and progress overview: `docs/system-architecture-and-progress.zh-CN.md`

## v1 principles

- Feishu group chat first
- one room equals one session
- `person_id` uses Feishu `open_id`
- `room_id` uses Feishu `chat_id`
- memory is optional and pluggable
- MemOS is a plugin, not a hard dependency

## Current layout

- `cmd/goclaw`: CLI entrypoint
- `internal/channels`: provider-agnostic channel contracts and adapters
- `internal/config`: runtime config types
- `internal/domain`: core business types
- `internal/feishu`: Feishu raw event types and normalization
- `internal/model`: model provider abstraction
- `internal/memory`: memory provider abstraction
- `internal/runtime`: application bootstrap and wiring
- `internal/store/sqlite`: SQLite store boundary
- `internal/mcp`: MCP stdio client and provider adapter
- `internal/skills`: prompt-only skill loader

Feishu connector details live in:

- `docs/feishu-connector.md`

## Current state

- SQLite schema and repositories are implemented.
- Feishu messages and IM events normalize into typed inbound channel messages/events.
- Feishu webhook handler supports `url_verification` plus typed IM event delivery for:
  - `im.message.receive_v1`
  - `im.message.recalled_v1`
  - `im.message.reaction.created_v1`
  - `im.message.reaction.deleted_v1`
  - `im.chat.member.bot.added_v1`
  - `im.chat.member.bot.deleted_v1`
  - `im.chat.member.user.added_v1`
  - `im.chat.member.user.deleted_v1`
  - `im.chat.member.user.withdrawn_v1`
- Feishu long connection mode supports the same IM event set over the official Feishu WebSocket transport.
- Feishu webhook supports request signature verification and encrypted event payloads.
- Channel runtime now persists non-message Feishu events and syncs recalled messages back into the local transcript.
- Feishu IM management now includes typed API and CLI support for:
  - chat metadata reads
  - chat member listing
  - message update / recall
  - message reactions
  - message pins
- Feishu channel now includes scope and capability diagnostics so IM permission state can be inspected without changing the global prompt/runtime contract.
- Model system prompts now disclose the active chat channel context, including provider/profile metadata plus high-level channel guidance for the current room.
- Prompt builder assembles transcript, retrieval scopes, and recalled memory into a model-facing package.
- Reply service can generate an assistant response, send it back to Feishu, and persist the assistant transcript.
- Model adapters now support one generic `openai-compatible` runtime for `openai-completions`, `openai-responses`, and `openai-codex-responses`, plus a native `anthropic` runtime for `anthropic-messages`.
- Consent policies can now be inspected and updated from the CLI.
- Room sessions now maintain a rolling summary plus a compact recent transcript window.
- Room kind is stored explicitly so consent defaults can differ between group and direct chats.
- Memory remains pluggable and defaults to a no-op provider.
- An optional `MemOS` provider now supports scope-aware recall plus deferred writeback jobs for:
  - `room`
  - `person_summary`
  - `person_private`
- The current memory stack now combines:
  - person-first isolation
  - logical conversation memory settings
  - workspace-backed durable Markdown files
  - optional provider episodic memory
- A workspace-backed Markdown layer is now available for:
  - `profiles/<profile_id>/AGENTS.md`
  - `profiles/<profile_id>/SOUL.md`
  - `profiles/<profile_id>/IDENTITY.md`
  - `profiles/<profile_id>/TOOLS.md`
  - `persons/<person_id>/USER.md`
  - `persons/<person_id>/SUMMARY.md`
  - `persons/<person_id>/MEMORY.md`
  - `rooms/<room_id>/ROOM.md`
  - `conversations/<room_id>/<conversation_slug>/CONVERSATION.md`
  - `rooms/<room_id>/MEMORY.md`
  - `conversations/<room_id>/<conversation_slug>/MEMORY.md`
- Markdown recall now searches both stable `MEMORY.md` files and dated journal files under `memory/*.md`, then merges those hits with provider recall.
- For person durable Markdown memory, `summary` consent now reads `persons/<person_id>/SUMMARY.md`, while `full` consent additionally reads `persons/<person_id>/MEMORY.md` plus `persons/<person_id>/memory/*.md`.
- Provider memory writes are now queued into `memory_jobs` and drained by a background worker during `goclaw serve`, so reply latency is no longer tied to provider writeback.
- Markdown journal writes are now also queued into `memory_jobs` and materialized into local `memory/YYYY-MM-DD.md` files for:
  - `conversation`
  - `room`
  - `person_private`
  when `journal_mode` is `markdown_only` or `both`.
- Durable Markdown promotion is now available through `goclaw memory promote` for:
  - `conversation` -> `conversations/<room_id>/<conversation_slug>/MEMORY.md`
  - `room` -> `rooms/<room_id>/MEMORY.md`
  - `person_summary` -> `persons/<person_id>/SUMMARY.md`
  - `person_private` -> `persons/<person_id>/MEMORY.md`
  with duplicate fact suppression so repeated manual promotions do not keep re-appending the same fact.
- Durable Markdown promotion can now run in two modes:
  - direct write: `goclaw memory promote ...`
  - queued background write: `goclaw memory promote --defer ...`
- Queued durable promotion jobs can now be drained explicitly with:
  - `goclaw memory jobs run-once`
  and `goclaw serve` will also drain them through the existing `MemoryJobWorker`.
- Conservative auto-promote is now available for explicit scoped remember markers in user messages. It is:
  - globally gated by `GOCLAW_MEMORY_AUTO_PROMOTE_ENABLED`
  - weak-pattern gated by `GOCLAW_MEMORY_AUTO_PROMOTE_WEAK_PATTERNS_ENABLED`
  - stronger-pattern gated by `GOCLAW_MEMORY_AUTO_PROMOTE_STRONG_PATTERNS_ENABLED`
  - source-gated by:
    - `GOCLAW_MEMORY_AUTO_PROMOTE_SOURCE_TRANSCRIPT_ENABLED`
    - `GOCLAW_MEMORY_AUTO_PROMOTE_SOURCE_JOURNALS_ENABLED`
  - evidence-gated by `GOCLAW_MEMORY_AUTO_PROMOTE_MINIMUM_EVIDENCE`
  - layer-gated by:
    - `GOCLAW_MEMORY_AUTO_PROMOTE_CONVERSATION_ENABLED`
    - `GOCLAW_MEMORY_AUTO_PROMOTE_ROOM_ENABLED`
    - `GOCLAW_MEMORY_AUTO_PROMOTE_PERSON_SUMMARY_ENABLED`
    - `GOCLAW_MEMORY_AUTO_PROMOTE_PERSON_PRIVATE_ENABLED`
  - manually triggerable with `goclaw memory compact run-once`
- The current auto-promote parser only extracts explicit markers such as:
  - `remember summary: ...`
  - `remember room: ...`
  - `remember conversation: ...`
  - `remember private: ...`
  - `记住摘要: ...`
  - `记住群: ...`
  - `记住会话: ...`
  - `记住私有: ...`
- When `GOCLAW_MEMORY_AUTO_PROMOTE_WEAK_PATTERNS_ENABLED=true`, the parser will also recognize a small opt-in set of clearer natural-language patterns such as:
  - `我偏好...`
  - `我喜欢...`
  - `请叫我...`
  - `这个群主要是...`
  - `这次会话主要是...`
  - `I prefer ...`
  - `Please call me ...`
  - `This room is for ...`
  - `This conversation is about ...`
- When `GOCLAW_MEMORY_AUTO_PROMOTE_STRONG_PATTERNS_ENABLED=true`, GoClaw will also run a stronger rule-based extractor over recent transcript plus optional workspace journals. It still does not use model inference, and it only promotes facts once they meet the configured evidence threshold.
- The stronger extractor currently targets durable facts like:
  - user preferences and preferred names
  - room purpose / room responsibility
  - conversation focus / current goal
- Journal evidence is de-duplicated against identical recent transcript content so one message does not instantly count twice just because it was already journaled.
- Weak-pattern parsing still does not infer `person_private` memory from ordinary chat. Private durable memory remains explicit-only.
- When stronger auto-promote hits a typed durable conflict, it no longer writes straight through. It now queues a review job that can be inspected and resolved with:
  - `goclaw memory review list`
  - `goclaw memory review approve --job-id <id>`
  - `goclaw memory review reject --job-id <id>`
- Approving a review now resolves the durable file directly: it retires the conflicting typed fact and writes the approved replacement instead of simply appending both.
- Room sessions now persist an `active_conversation_id`, so one room can explicitly select which logical conversation profile drives retrieval and writeback.
- Conversation state can now be managed from the CLI with:
  - `goclaw conversations list`
  - `goclaw conversations get`
  - `goclaw conversations create`
  - `goclaw conversations activate`
  - `goclaw conversations settings get`
  - `goclaw conversations settings set`
- Model requests now include a formal capability prompt that tells the model about:
  - visible tools
  - visible prompt-only skills
  - memory scopes
  - consent-driven personal memory limits
  - final-only delivery constraints
- Room-scoped tool permissions can now be stored, resolved, and checked for:
  - commands
  - filesystem paths
  - network hosts
- A guarded local tool runtime is now available through the CLI for:
  - command execution
  - file reads
  - file writes
  - HTTP fetches
- MCP stdio servers can now be exposed as room-governed tools through the same `agenttools` registry and audit path.
- Workspace skills can now be loaded from `skills/*/SKILL.md` and injected into the system prompt as guidance-only capability blocks.
- The reply loop now supports native `Anthropic` tool calling under room policy, with the older guarded JSON tool protocol retained as a fallback for providers that do not expose native tools yet.

## Configuration model

GoClaw now loads runtime config in this order:

1. built-in defaults
2. optional JSON config file from `--config`, `GOCLAW_CONFIG_PATH`, or `GOCLAW_CONFIG`
3. environment variable overrides

The stable shape for chat carriers is now:

```json
{
  "channels": {
    "feishu": {
      "enabled": true,
      "defaultAccount": "main",
      "connectionMode": "longpoll",
      "renderMode": "auto",
      "actions": {
        "processingAck": true,
        "messageSend": true,
        "messageUpdate": true,
        "messageRecall": true,
        "reactions": true,
        "pins": true,
        "postMessages": true,
        "docsRead": true
      },
      "accounts": {
        "main": {
          "profileId": "main",
          "renderMode": "auto",
          "processingAckEmoji": "EYES",
          "actions": {
            "processingAck": false
          },
          "appId": "cli_test",
          "appSecret": "secret_test"
        }
      }
    }
  }
}
```

The config file can now also expose workspace-backed skills and MCP servers:

```json
{
  "workspace": {
    "root": "var/workspace"
  },
  "mcp": {
    "enabled": true,
    "servers": {
      "github": {
        "command": "github-mcp-server",
        "args": ["stdio"],
        "safetyClass": "read_only",
        "toolPrefix": "github"
      }
    }
  },
  "skills": {
    "enabled": true,
    "root": "var/workspace/skills",
    "maxEntries": 32,
    "maxInline": 4
  }
}
```

Feishu-specific env overrides now support both styles:

- new nested style:
  - `GOCLAW_CHANNELS_FEISHU_*`
  - `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_<ID>_*`
- backward-compatible legacy style:
  - `GOCLAW_FEISHU_*`

Account IDs in env vars should use lowercase snake-case in config and uppercase snake-case in env names, for example:

```bash
export GOCLAW_CHANNELS_FEISHU_DEFAULT_ACCOUNT=main
export GOCLAW_CHANNELS_FEISHU_RENDER_MODE=auto
export GOCLAW_CHANNELS_FEISHU_ACTIONS_REACTIONS=true
export GOCLAW_CHANNELS_FEISHU_ACTIONS_DOCS_READ=true
export GOCLAW_CHANNELS_FEISHU_PROCESSING_ACK_EMOJI=EYES
export GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_PROFILE_ID=main
export GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_RENDER_MODE=auto
export GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_ACTIONS_PROCESSING_ACK=false
export GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_APP_ID=cli_test
export GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_APP_SECRET=secret_test
```

## Run the Feishu server

`goclaw serve` now runs all enabled channel transports from the channel registry.

Feishu currently supports two inbound transports:

- `webhook`: local HTTP listener, compatible with Feishu event callback mode
- `longpoll`: Feishu long connection over WebSocket, no public webhook URL required

Recommended production entrypoint is a JSON config file plus secrets injected by env:

An example file lives at `goclaw.example.json`.

```json
{
  "database": {
    "path": "var/goclaw.db"
  },
  "channels": {
    "feishu": {
      "enabled": true,
      "defaultAccount": "main",
      "connectionMode": "longpoll",
      "renderMode": "auto",
      "processingAckEmoji": "EYES",
      "accounts": {
        "main": {
          "profileId": "main",
          "renderMode": "auto",
          "processingAckEmoji": "DONE"
        }
      }
    }
  },
  "model": {
    "provider": "openai-compatible",
    "api": "openai-completions",
    "baseUrl": "https://api.openai.com/v1",
    "modelId": "gpt-4.1-mini"
  }
}
```

```bash
export GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_APP_ID=cli_test
export GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_APP_SECRET=secret_test
export GOCLAW_CHANNELS_FEISHU_RENDER_MODE=auto
export GOCLAW_CHANNELS_FEISHU_PROCESSING_ACK_EMOJI=EYES
export GOCLAW_MODEL_API_KEY=replace-model-key

go run ./cmd/goclaw --config goclaw.json serve
```

## Run in webhook mode

Switch the top-level or account-level `connectionMode` to `webhook` and add the webhook secrets:

```bash
export GOCLAW_CHANNELS_FEISHU_CONNECTION_MODE=webhook
export GOCLAW_CHANNELS_FEISHU_ENCRYPT_KEY=replace-encrypt-key
export GOCLAW_CHANNELS_FEISHU_VERIFICATION_TOKEN=replace-me
```

Default webhook listener settings:

- host: `127.0.0.1`
- port: `3000`
- path: `/feishu/events`

Override them with either:

- `GOCLAW_CHANNELS_FEISHU_WEBHOOK_HOST`
- `GOCLAW_CHANNELS_FEISHU_WEBHOOK_PORT`
- `GOCLAW_CHANNELS_FEISHU_WEBHOOK_PATH`

or per-account env overrides:

- `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_WEBHOOK_HOST`
- `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_WEBHOOK_PORT`
- `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_WEBHOOK_PATH`

## Run in longpoll mode

In `longpoll` mode, `EncryptKey`, `VerificationToken`, and the local webhook bind settings are not used.

If your tenant uses a custom Feishu/Lark API base URL, keep setting:

- `GOCLAW_CHANNELS_FEISHU_API_BASE_URL`
- `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_API_BASE_URL`

`processingAckEmoji` controls the temporary reaction GoClaw adds while a reply is being generated and removes after the reply finishes. You can set it either:

- top-level: `GOCLAW_CHANNELS_FEISHU_PROCESSING_ACK_EMOJI`
- per-account: `GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_PROCESSING_ACK_EMOJI`

Account-level values override the top-level value. The default is `THUMBSUP`.

`renderMode` controls how GoClaw renders Feishu replies:

- `auto`: use interactive cards for fenced code blocks or markdown tables, otherwise use post messages
- `card`: force interactive cards first
- `raw`: force post messages first

GoClaw still degrades on failure:

- `interactive` -> `post`
- `post` -> `text`

## Feishu event subscriptions

To enable the full IM event pipeline, subscribe these Feishu events in the app console:

- `im.message.receive_v1`
- `im.message.recalled_v1`
- `im.message.reaction.created_v1`
- `im.message.reaction.deleted_v1`
- `im.chat.member.bot.added_v1`
- `im.chat.member.bot.deleted_v1`
- `im.chat.member.user.added_v1`
- `im.chat.member.user.deleted_v1`
- `im.chat.member.user.withdrawn_v1`

`im.message.receive_v1` enters the reply loop. The other events are persisted through the channel event pipeline, and `im.message.recalled_v1` additionally updates stored transcript messages to `[recalled]`.

## Feishu IM CLI

GoClaw now exposes first-class Feishu IM operations through the CLI:

```bash
go run ./cmd/goclaw --config goclaw.json feishu chat get --profile-id main --chat-id oc_xxx
go run ./cmd/goclaw --config goclaw.json feishu chat members --profile-id main --chat-id oc_xxx
go run ./cmd/goclaw --config goclaw.json feishu message send --profile-id main --chat-id oc_xxx --text "hello"
go run ./cmd/goclaw --config goclaw.json feishu message send --profile-id main --chat-id oc_xxx --msg-type interactive --content-json '{"type":"template","data":{"template_id":"ctp_xxx"}}'
go run ./cmd/goclaw --config goclaw.json feishu message reply --profile-id main --message-id om_xxx --text "reply"
go run ./cmd/goclaw --config goclaw.json feishu message forward --profile-id main --message-id om_src --chat-id oc_xxx
go run ./cmd/goclaw --config goclaw.json feishu message merge-forward --profile-id main --message-ids om_a,om_b --chat-id oc_xxx
go run ./cmd/goclaw --config goclaw.json feishu thread forward --profile-id main --thread-id th_xxx --chat-id oc_xxx
go run ./cmd/goclaw --config goclaw.json feishu message follow-up --profile-id main --message-id om_xxx --follow-ups-json '[{"content":"step 1"}]'
go run ./cmd/goclaw --config goclaw.json feishu message urgent-app --profile-id main --message-id om_xxx --user-ids ou_xxx
go run ./cmd/goclaw --config goclaw.json feishu message update --profile-id main --message-id om_xxx --text "updated"
go run ./cmd/goclaw --config goclaw.json feishu message recall --profile-id main --message-id om_xxx
go run ./cmd/goclaw --config goclaw.json feishu reaction list --profile-id main --message-id om_xxx
go run ./cmd/goclaw --config goclaw.json feishu reaction add --profile-id main --message-id om_xxx --emoji-type THUMBSUP
go run ./cmd/goclaw --config goclaw.json feishu reaction delete --profile-id main --message-id om_xxx --reaction-id re_xxx
go run ./cmd/goclaw --config goclaw.json feishu pin list --profile-id main --chat-id oc_xxx
go run ./cmd/goclaw --config goclaw.json feishu pin add --profile-id main --message-id om_xxx
go run ./cmd/goclaw --config goclaw.json feishu pin delete --profile-id main --message-id om_xxx
go run ./cmd/goclaw --config goclaw.json feishu scopes list --profile-id main
go run ./cmd/goclaw --config goclaw.json feishu capabilities status --profile-id main
```

These commands currently resolve the Feishu account by `profile-id` and use tenant credentials from the configured channel account.

`feishu scopes list` returns the raw Feishu app scopes reported by `application/v6/scopes`. `feishu capabilities status` maps those scopes onto GoClaw's current Feishu IM capability matrix.

GoClaw does not inject raw Feishu scopes into the model system prompt. These scope and capability diagnostics stay inside the Feishu channel/CLI boundary.

## Anthropic example

For Anthropic-native transport instead, keep the same `goclaw.json` structure and override only the model secrets:

```bash
export GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_APP_ID=cli_test
export GOCLAW_CHANNELS_FEISHU_ACCOUNTS_MAIN_APP_SECRET=secret_test
export GOCLAW_MODEL_PROVIDER=anthropic
export GOCLAW_MODEL_API=anthropic-messages
export GOCLAW_MODEL_BASE_URL=https://api.anthropic.com
export GOCLAW_MODEL_API_KEY=replace-anthropic-key
export GOCLAW_MODEL_ID=claude-sonnet-4-5
export GOCLAW_MODEL_MAX_TOKENS=2048
export GOCLAW_ANTHROPIC_VERSION=2023-06-01

go run ./cmd/goclaw --config goclaw.json serve
```

For local or proxy backends that do not require bearer auth, set:

- `GOCLAW_MODEL_AUTH_HEADER=false`

To enable the optional `MemOS` plugin, add:

- `GOCLAW_MEMORY_PROVIDER=memos`
- `GOCLAW_MEMOS_ENABLED=true`
- `GOCLAW_MEMOS_BASE_URL=https://memos.memtensor.cn/api/openmem/v1`
- `GOCLAW_MEMOS_API_KEY=replace-memos-key`

To enable workspace-backed Markdown rules and durable memory, add:

- `GOCLAW_WORKSPACE_ROOT=var/workspace`

To enable MCP and workspace skills, add:

- `GOCLAW_MCP_ENABLED=true`
- `GOCLAW_SKILLS_ENABLED=true`
- `GOCLAW_SKILLS_ROOT=var/workspace/skills`

The current workspace skill layout is:

- `skills/<skill_name>/SKILL.md`

Current extension inventory can be inspected with:

```bash
go run ./cmd/goclaw mcp list
go run ./cmd/goclaw skills list
```

Room permissions now cover three execution surfaces:

- local commands, filesystem paths, and network hosts
- channel tools
- MCP tools and MCP servers

Tool permissions can be inspected and updated with:

```bash
go run ./cmd/goclaw permissions get --profile-id main --room-id oc_room_1
go run ./cmd/goclaw permissions set --profile-id main --room-id oc_room_1 \
  --commands-mode allow_list \
  --paths-mode allow_list \
  --network-mode allow_list \
  --allow-commands git,bash \
  --allow-paths /tmp/work \
  --allow-hosts api.anthropic.com
go run ./cmd/goclaw permissions set --profile-id main --room-id oc_room_1 \
  --mcp-read-mode allow_list \
  --allow-mcp-servers github
go run ./cmd/goclaw permissions check --profile-id main --room-id oc_room_1 \
  --kind command \
  --command "git status"
```

Once MCP permissions are granted, MCP tools show up in the model-visible capability prompt and in tooling audit rows with `tool_source=mcp`.

Guarded local tools can then be run with:

```bash
go run ./cmd/goclaw tools exec --profile-id main --room-id oc_room_1 --workdir /tmp/work -- git status
go run ./cmd/goclaw tools read --profile-id main --room-id oc_room_1 --path /tmp/work/notes.txt
go run ./cmd/goclaw tools write --profile-id main --room-id oc_room_1 --path /tmp/work/notes.txt --content "hello" --mkdir
go run ./cmd/goclaw tools fetch --profile-id main --room-id oc_room_1 --url https://api.anthropic.com/v1/messages
```

When a room has explicit tool permissions, the model reply loop can also request those same tools through the guarded runtime:

- native `Anthropic` providers now use real `tool_use` / `tool_result` turns
- other providers still fall back to the guarded JSON tool protocol

## Channel architecture

The runtime no longer hardcodes Feishu directly. The stable extension boundary is now:

- `internal/channels/types.go`
- `internal/channels/registry.go`
- `internal/channels/<provider>`

Each channel implementation is responsible for:

- config interpretation
- inbound transport setup
- message normalization into `InboundMessage`
- outbound sends

The runtime only knows how to run channel transports and ingest normalized messages. New platforms should follow the same structure instead of adding provider-specific branches under `internal/runtime`.

## Next steps

1. Add streaming support to the model adapter layer.
2. Add diff/preview output for review approvals so operators can inspect exactly which durable facts will be retired before writing.
3. Extend native tool-calling beyond `Anthropic` where it is worth the complexity.
