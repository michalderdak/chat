# Unary TUI Mode Design Spec

## Overview

Add a `--unary` mode to the Go TUI client that uses `SendMessage` unary RPCs instead of bidi streaming. Each user message is a separate RPC, making Envoy's per-RPC round-robin load balancing visible — each response comes from a different pod, shown in the event log.

## Server Change

In `SendMessage` handler (`service.py`), set trailing metadata with the pod hostname:

```python
await context.set_trailing_metadata([("x-served-by", os.getenv("HOSTNAME", "unknown"))])
```

No proto changes. gRPC metadata is like HTTP headers — carried alongside the response.

## Go Client Changes

### New flag

`--unary` flag in `main.go`. When set, launches unary TUI model instead of bidi model. No bidi stream opened.

### Unary TUI (`client/tui/unary_model.go`)

Same split-screen layout as bidi model:
- Left panel: chat messages (complete responses, no streaming cursor)
- Right panel: event log showing RPC pairs with pod name and duration
- Status bar: context info, no streaming indicator
- Enter: sends `SendMessage` RPC, waits for response, displays it

Simpler than bidi model — no stream state, no reconnect logic, no cancel, no heartbeat.

### Unary commands (`client/tui/unary_commands.go`)

`SendUnary` command:
1. Calls `ChatServiceClient.SendMessage()` unary RPC
2. Extracts `x-served-by` from trailing metadata via `grpc.Trailer()` call option
3. Returns a message with response text, pod name, and duration

### Event log entries

```
→ SendMessage "What is gRPC?"
← Response (chat-server-5md2x) 1.2s
→ SendMessage "Tell me more"
← Response (chat-server-9zstr) 0.9s
```

Each RPC is two lines — outgoing request and incoming response with pod name. The pod name changing between requests visually demonstrates round-robin.

### main.go

```
if *unary {
    // Create ChatServiceClient only (no stream)
    model := tui.NewUnaryModel(client, conversationID)
    // Launch TUI
} else {
    // Existing bidi flow
}
```

### Makefile targets

- `make unary-grpc` — `go run ./client/ --unary --target localhost:50051 --token demo-token`
- `make unary-envoy` — `go run ./client/ --unary --target localhost:50052 --tls --ca-cert ... --client-cert ... --client-key ...`

## Demo Flow

1. Run `make unary-envoy`
2. Send 3-4 messages
3. Event log shows each response from a different pod (round-robin)
4. Run `make unary-grpc`
5. Send 3-4 messages
6. All responses from the same pod (pick-first on a single connection)

The contrast is immediate and visible in the TUI.
