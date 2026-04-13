# Unified TUI Design Spec

## Overview

Merge the four separate client modes (gRPC unary, gRPC stream, Envoy unary, Envoy stream) into a single TUI with tab switching. Two gRPC connections (one plaintext, one mTLS) are created at startup and shared across modes. Tab key cycles between modes. Each mode maintains its own chat history, event log, and stream state.

## Connection Model

Two `grpc.ClientConn` created at startup:
- `grpcConn` — plaintext to `--grpc-target` (default `localhost:50051`) with bearer token
- `envoyConn` — mTLS to `--envoy-target` (default `localhost:50052`) with CA + client certs

Each connection creates a `ChatServiceClient`. Both clients stay alive for the entire session. If a connection fails (e.g., Envoy not deployed), those tabs show an error message instead of crashing.

## Four Modes

| Mode | Connection | RPC Type | State |
|---|---|---|---|
| gRPC Unary | grpcConn | SendMessage | messages, eventLog |
| gRPC Stream | grpcConn | Chat bidi | messages, eventLog, stream, reconnect |
| Envoy Unary | envoyConn | SendMessage | messages, eventLog |
| Envoy Stream | envoyConn | Chat bidi | messages, eventLog, stream, reconnect |

Each mode has independent:
- `messages []ChatMessage` — chat history
- `eventLog []EventEntry` — protocol events
- `conversationID` — unique per mode (generated at startup)
- `stream *StreamClient` — only for bidi modes, opened lazily on first message
- `podName` — from initial/trailing metadata

## Tab Navigation

- **Tab key** cycles forward: gRPC Unary → gRPC Stream → Envoy Unary → Envoy Stream → (wrap)
- Active tab highlighted in the tab bar, others dimmed
- Tab bar rendered at top of the TUI:
  ```
  [gRPC Unary]  [gRPC Stream]  [Envoy Unary]  [Envoy Stream]
  ```
- Background bidi streams keep running when tabbed away (tokens accumulate)
- Switching back to a tab shows accumulated messages

## TUI Layout

```
┌─[gRPC Unary]──[gRPC Stream]──[Envoy Unary]──[Envoy Stream]──┐
│  ┌── Chat ──────────────┬── gRPC Event Log ──────────────┐   │
│  │                      │                                 │   │
│  │                      │                                 │   │
│  ├──────────────────────┴─────────────────────────────────┤   │
│  │ status bar with mode, pod, usage                       │   │
│  ├────────────────────────────────────────────────────────┤   │
│  │ > input (Tab to switch, Esc to quit)                   │   │
│  └────────────────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────────────┘
```

Same split-screen layout as before (chat left, event log right). Only the tab bar and mode-specific state change.

## main.go Changes

New flags:
- `--grpc-target` (replaces `--target`, default `localhost:50051`)
- `--envoy-target` (default `localhost:50052`)
- Remove `--unary` flag
- Keep `--token`, `--tls` (now always true for envoy), `--ca-cert`, `--client-cert`, `--client-key`, `--timeout`

Creates both connections, both clients, passes them to a single unified model.

## Makefile

Single target:
```makefile
client:
	go run ./client/ \
		--grpc-target localhost:50051 --token demo-token \
		--envoy-target localhost:50052 \
		--ca-cert deploy/envoy/certs/generated/ca.crt \
		--client-cert deploy/envoy/certs/generated/client.crt \
		--client-key deploy/envoy/certs/generated/client.key
```

Remove `client-envoy`, `unary-grpc`, `unary-envoy` targets.

## File Changes

```
Modified:
  client/main.go              — two connections, unified model
  Makefile                     — single client target

New:
  client/tui/unified_model.go — UnifiedModel with tab switching and 4 mode states

Removed:
  client/tui/model.go         — replaced by unified_model.go
  client/tui/unary_model.go   — merged into unified_model.go
```

`commands.go`, `unary_commands.go`, `events.go`, `styles.go` stay unchanged — they define message types and rendering used by all modes.

## Mode State Struct

```go
type ModeState struct {
    Name           string                    // "gRPC Unary", "gRPC Stream", etc.
    Client         chatv1.ChatServiceClient  // shared per connection
    IsStream       bool                      // true for bidi modes
    Messages       []ChatMessage
    EventLog       []EventEntry
    Stream         *grpcclient.StreamClient  // nil for unary, lazy-opened for bidi
    ConversationID string
    PodName        string
    Streaming      bool                      // actively generating (bidi)
    Waiting        bool                      // waiting for unary response
    Reconnecting   bool
    PromptTokens   int
    CompletionTokens int
    ContextLength  int
}
```

The `UnifiedModel` holds `modes [4]*ModeState` and `activeMode int`.

## Key Design Decisions

1. **Two connections, four modes** — gRPC multiplexes streams on one connection. Unary and bidi share the same `ClientConn`. This is the correct HTTP/2 pattern.
2. **Lazy stream opening** — bidi streams opened on first message, not at startup. Avoids wasting resources on unused modes.
3. **Independent state per mode** — switching tabs doesn't affect other modes. Background streams keep running.
4. **Graceful degradation** — if Envoy connection fails, those tabs show an error, other tabs still work.
5. **Single make target** — all flags passed at once. Simpler for demo.
