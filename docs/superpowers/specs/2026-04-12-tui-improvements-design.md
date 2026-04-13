# TUI Improvements Design Spec

## Overview

Improve the Go Bubble Tea TUI and Python server to provide a richer demo experience: split-screen layout with gRPC event log, mid-generation interrupt, server-side conversation history, context window usage reporting, and playful heartbeat words.

## Proto Changes

Add `UsageInfo` message to `ChatResponse` oneof (field 7):

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
  }
}

message UsageInfo {
  int32 prompt_tokens = 1;
  int32 completion_tokens = 2;
  int32 context_length = 3;
}
```

Add `beat` field to `Heartbeat`:

```protobuf
message Heartbeat {
  string beat = 1;
}
```

Backwards-compatible — old clients ignore unknown oneof fields.

## Python Server Changes

### Conversation History (`service.py`)

- `Chat` handler maintains `conversation_history: list[dict]` for the stream session
- Each `UserMessage` appends `{"role": "user", "content": text}`
- After generation completes, appends `{"role": "assistant", "content": full_response}` (accumulated from all tokens)
- History passed to `ollama.chat()` on each call
- On cancel, partial response is still appended to history (the model should know what it said so far)

### Ollama Client (`ollama.py`)

- `chat()` accepts full conversation history (already has the parameter, just not used by service.py)
- Capture `prompt_eval_count` and `eval_count` from the final `done: true` NDJSON chunk
- Return these as a dataclass or tuple alongside the token stream
- New method `get_model_context_length() -> int`: calls `GET /api/show` with model name, returns `model_info["qwen3.context_length"]` (or generic key pattern `*.context_length`). Called once at server startup, cached.
- New method `generate_heartbeat_word() -> str`: calls `/api/generate` with prompt like "Output a single creative playful gerund word like shimmering or gallivanting. Only the word, nothing else." with `stream: false`. Returns the single word stripped of whitespace.

### Usage Reporting

- `_generate` captures the final usage stats from `ollama.chat()`
- Sends `UsageInfo(prompt_tokens=..., completion_tokens=..., context_length=cached_value)` right before `PHASE_DONE`

### Heartbeat

- When a bidi stream opens, start a background `asyncio.Task` that every 15 seconds:
  1. Calls `ollama.generate_heartbeat_word()` to get a fresh playful word
  2. Puts `ChatResponse(heartbeat=Heartbeat(beat=word))` into the send queue
- Task is cancelled when the stream closes
- If the heartbeat Ollama call fails (e.g., Ollama busy), send `Heartbeat(beat="...")` as fallback

## Go TUI Changes

### Split-Screen Layout (`model.go`)

```
┌──────────────────────────┬──────────────────────────────┐
│  Chat                    │  gRPC Event Log              │
│                          │                              │
│  You: What is gRPC?      │  → UserMessage "What is..."  │
│                          │  ← StatusUpdate THINKING     │
│  AI: gRPC is a high...▌  │  ← StatusUpdate GENERATING   │
│                          │  ← Token "gRPC"              │
│                          │  ← Token " is"               │
│                          │  ← Heartbeat "shimmering"    │
│                          │  ← UsageInfo 42+187/40960    │
│                          │  ← StatusUpdate DONE         │
├──────────────────────────┴──────────────────────────────┤
│  tokens: 229/40960 (0.6%) | ready | streaming: false    │
├─────────────────────────────────────────────────────────┤
│  > Type a message...                                    │
└─────────────────────────────────────────────────────────┘
```

- Two `viewport.Model` instances: left for chat, right for event log
- Joined horizontally with lipgloss `JoinHorizontal` at 50/50 split
- Both scrollable independently (event log auto-scrolls to bottom)
- Status bar shows context usage from last `UsageInfo`
- Text input at bottom spanning full width

### Event Log (`events.go` — new file)

Each event is a single compact line with direction arrow and type:

**Format:** `← TypeName payload_summary`

**Color scheme:**

| Event | Color |
|---|---|
| `→ UserMessage` | Green |
| `→ CancelGeneration` | Yellow |
| `→ ContextInjection` | Cyan |
| `← Token` | Dim white (high volume, recedes visually) |
| `← StatusUpdate` | Magenta (phase transitions pop) |
| `← Error` | Red bold |
| `← Heartbeat` | Dim yellow |
| `← Acknowledgement` | Cyan |
| `← UsageInfo` | Blue |

Token text truncated to 30 chars in the log. All other payloads shown in full.

### Mid-Generation Interrupt (`model.go`)

- Remove the `if m.streaming { return }` guard on Enter key
- When Enter is pressed while streaming:
  1. Send `CancelGeneration` on the stream
  2. Mark current assistant message as complete (remove cursor)
  3. Append `[cancelled]` suffix to the partial response
  4. Append new user message + empty assistant message
  5. Send new `UserMessage` on the stream
  6. Continue listening with `WaitForEvent`
- All events (cancel sent, ack received, new message sent) appear in the event log
- The server already handles this correctly: `user_message` action cancels in-flight generation and starts a new one

### Commands (`commands.go`)

- New `UsageMsg` type: `{ PromptTokens, CompletionTokens, ContextLength int }`
- `WaitForEvent` handles `ChatResponse_Usage` → returns `UsageMsg`
- Every `Send*` function also appends to a shared event log (passed as parameter or via channel)

### Model State

New fields on `Model`:
- `eventLog []EventEntry` — all protocol events
- `eventViewport viewport.Model` — right panel
- `promptTokens, completionTokens, contextLength int` — last known usage

## Key Design Decisions

1. **Server-side history** — standard for long-lived stream sessions, no proto changes needed for history
2. **Single compact line per event** — keeps the event log dense so it doesn't overwhelm the chat panel
3. **Dim tokens** — high-volume token events visually recede, letting protocol events (cancel, ack, status) stand out
4. **Auto-cancel on new message** — the client sends cancel then new message; the server's existing bidi logic handles this (cancel in-flight, start new generation)
5. **Heartbeat word per beat** — each heartbeat is a fresh Ollama call for a playful word, making the event log more entertaining for demo audiences
