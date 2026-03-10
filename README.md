# GoClaw

GoClaw is a Go-based chat runtime built around person-centered isolation.

## v1 principles

- Feishu group chat first
- one room equals one session
- `person_id` uses Feishu `open_id`
- `room_id` uses Feishu `chat_id`
- memory is optional and pluggable
- MemOS is a plugin, not a hard dependency

## Current layout

- `cmd/goclaw`: CLI entrypoint
- `internal/config`: runtime config types
- `internal/domain`: core business types
- `internal/feishu`: Feishu-facing normalized message types
- `internal/memory`: memory provider abstraction
- `internal/runtime`: application bootstrap and wiring
- `internal/store/sqlite`: SQLite store boundary

## Next steps

1. Add SQLite migrations and repositories.
2. Add Feishu webhook adapter.
3. Add prompt builder and room session manager.
4. Add optional MemOS provider.
