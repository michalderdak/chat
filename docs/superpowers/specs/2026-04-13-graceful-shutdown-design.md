# Graceful Shutdown & Redis History Design Spec

## Overview

Add graceful shutdown to the gRPC chat server so that during rolling K8s deployments, active streams finish their current generation, persist conversation history to Redis, notify clients to reconnect, and resume seamlessly on a new pod. The Go TUI client auto-reconnects and the event log shows the full lifecycle without token noise.

## Proto Changes

Add `ServerShutdown` message (field 8) to `ChatResponse` oneof:

```protobuf
message ChatResponse {
  string conversation_id = 1;
  oneof event {
    Token token = 2;
    StatusUpdate status = 3;
    Error error = 4;
    Heartbeat heartbeat = 5;
    Acknowledgement ack = 6;
    UsageInfo usage = 7;
    ServerShutdown shutdown = 8;
  }
}

message ServerShutdown {
  string reason = 1;
}
```

Add storage messages (not used in RPCs, only for Redis serialization):

```protobuf
message ConversationMessage {
  string role = 1;
  string content = 2;
}

message ConversationHistory {
  repeated ConversationMessage messages = 1;
}
```

## Redis Setup

**Deployment:** Single `redis:7-alpine` pod per namespace, no persistence, added to base kustomization. ClusterIP service on port 6379.

**Data model:**
- Key: `chat:history:{conversation_id}`
- Value: protobuf-serialized `ConversationHistory` bytes
- TTL: 1 hour
- Server writes to Redis on every completed generation (always fresh)
- On stream open, server loads existing history from Redis if present

**Config:** New env var `REDIS_URL` defaulting to `redis://redis:6379`.

**Python dependency:** `redis[hiredis]` added to `pyproject.toml`, using `redis.asyncio` client.

## Server Graceful Shutdown

### SIGTERM handler (`main.py`)

1. Catch SIGTERM via `asyncio` signal handler
2. Set health status to `NOT_SERVING` (K8s readiness probe fails, stops new traffic)
3. Call `servicer.drain()` — waits for active generations to finish, saves history, sends shutdown signal
4. Call `server.stop(grace=30)` — 30s for streams to close after receiving shutdown
5. Exit

### Servicer drain (`service.py`)

- Servicer tracks all active streams (set of send queues + metadata)
- `drain()` method:
  1. Set a draining flag (prevents new generations from starting)
  2. Wait for any active generation task to complete naturally (don't cancel it — let tokens finish streaming and `PHASE_DONE` be sent)
  3. Save each stream's conversation history to Redis
  4. Send `ServerShutdown(reason="pod draining")` on each stream's send queue
  5. Send `None` sentinel to close each stream
- Safety: if a generation hasn't finished within 20s of drain starting, cancel it (don't exceed the 30s termination budget)

### Stream lifecycle with Redis

- **Stream open:** Check Redis for `chat:history:{conversation_id}`. If found, load into `conversation_history` list. The AI has full context from the previous pod.
- **After each generation:** Save `conversation_history` to Redis. Always fresh, not just on shutdown.
- **Stream close:** History stays in Redis with 1-hour TTL for potential reconnect.

## K8s Manifest Changes

### Server deployment (`deploy/base/server-deployment.yaml`)

```yaml
terminationGracePeriodSeconds: 30
lifecycle:
  preStop:
    exec:
      command: ["sleep", "5"]
```

The `preStop` sleep gives K8s time to update endpoints and Envoy to stop routing new RPCs before the server starts draining.

New env var:
```yaml
- name: REDIS_URL
  value: "redis://redis:6379"
```

### Redis deployment (`deploy/base/redis-deployment.yaml`)

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: redis
spec:
  replicas: 1
  selector:
    matchLabels:
      app: redis
  template:
    metadata:
      labels:
        app: redis
    spec:
      containers:
        - name: redis
          image: redis:7-alpine
          ports:
            - containerPort: 6379
```

### Redis service (`deploy/base/redis-service.yaml`)

```yaml
apiVersion: v1
kind: Service
metadata:
  name: redis
spec:
  selector:
    app: redis
  ports:
    - port: 6379
      targetPort: 6379
```

### PodDisruptionBudget (`deploy/base/pdb.yaml`)

```yaml
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: chat-server
spec:
  maxUnavailable: 1
  selector:
    matchLabels:
      app: chat-server
```

## Go Client Reconnect

### New message type

`ShutdownMsg{Reason string}` — returned by `WaitForEvent` when receiving `ChatResponse_Shutdown`.

### Reconnect flow (`model.go`)

1. Receive `ShutdownMsg`
2. Event log shows `← ServerShutdown "pod draining"` (red)
3. Chat panel shows system message: `[Server restarting, reconnecting...]`
4. Close current stream
5. Open new stream with same gRPC connection and same `conversation_id`
6. Event log shows `→ Reconnected` (green)
7. New pod loads history from Redis, AI has full context
8. Resume `WaitForEvent` loop

### Error reconnect

If stream dies with `UNAVAILABLE` without `ServerShutdown` (unexpected crash):
- Chat shows `[Connection lost, reconnecting...]`
- Same reconnect flow: close, reopen, same `conversation_id`

### Stream reopening

The `grpcclient.StreamClient` needs a `Reopen()` method (or the model creates a new `StreamClient` using the existing `ChatServiceClient`). The gRPC connection stays alive — only the stream (HTTP/2 stream) is reopened.

## Event Log Change

Remove `← Token` events from the event log display. Tokens still render in the chat panel. The event log only shows protocol-level events:

- `→ UserMessage`, `→ CancelGeneration`, `→ ContextInjection`
- `← StatusUpdate`, `← Error`, `← Heartbeat`, `← Acknowledgement`, `← UsageInfo`, `← ServerShutdown`
- `→ Reconnected`

## Key Design Decisions

1. **Wait for generation to finish before shutdown** — don't cut off the AI mid-response. The 30s termination budget is plenty for Qwen3-0.6B responses. Safety cancel at 20s.
2. **Protobuf serialization for Redis** — consistent with the project's "protobuf everywhere" approach. Smaller than JSON, schema-enforced.
3. **Write history after every generation, not just shutdown** — if the server crashes unexpectedly, history is still in Redis from the last completed response.
4. **maxUnavailable: 1 PDB** — at most 1 of 3 pods down during rollouts. Combined with graceful drain, minimizes disruption.
5. **preStop sleep: 5s** — gives K8s and Envoy time to update routing before the server starts draining.
6. **Auto-reconnect on UNAVAILABLE too** — handles both graceful and ungraceful disconnects. Same user experience either way.
