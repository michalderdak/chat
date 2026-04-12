# TUI Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add split-screen event log, mid-generation interrupt, conversation history, context window usage, and playful heartbeat words to the gRPC chat demo.

**Architecture:** Proto gets two additions (UsageInfo message, Heartbeat.beat field). Python server maintains conversation history per stream, reports usage stats, and sends heartbeat words from Ollama. Go TUI gets a split-screen layout with a colored event log panel showing every gRPC message.

**Tech Stack:** Protocol Buffers, Python grpc.aio, Go Bubble Tea + Lipgloss, httpx, Ollama API

---

## File Structure

```
Modified:
  proto/chat/v1/chat.proto          — add UsageInfo, Heartbeat.beat
  server/src/chat_server/ollama.py  — usage stats, context length, heartbeat word
  server/src/chat_server/service.py — conversation history, usage reporting, heartbeat task
  client/tui/styles.go              — event log color styles
  client/tui/commands.go            — UsageMsg type, event logging on send/recv
  client/tui/model.go               — split-screen, mid-gen interrupt, usage state

New:
  client/tui/events.go              — EventEntry type and rendering
```

---

### Task 1: Proto Changes & Code Regeneration

**Files:**
- Modify: `proto/chat/v1/chat.proto`

- [ ] **Step 1: Update chat.proto**

Add `string beat = 1` to `Heartbeat` and add `UsageInfo` message with field 7 in the `ChatResponse` oneof.

Replace the existing `Heartbeat` message and `ChatResponse` message:

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
```

Add after the `Acknowledgement` message:

```protobuf
message UsageInfo {
  int32 prompt_tokens = 1;
  int32 completion_tokens = 2;
  int32 context_length = 3;
}
```

Replace the empty `Heartbeat` message:

```protobuf
message Heartbeat {
  string beat = 1;
}
```

- [ ] **Step 2: Regenerate stubs**

```bash
make docker
```

Expected: `gen/go/chat/v1/` and `gen/python/chat/v1/` files are updated with `UsageInfo` and `Heartbeat.beat`.

- [ ] **Step 3: Verify**

```bash
buf lint
```

Expected: passes.

```bash
go build ./client/... ./gateway/...
```

Expected: compiles (Go code doesn't use the new types yet).

- [ ] **Step 4: Commit**

```bash
git add proto/ gen/
git commit -m "feat: add UsageInfo message and Heartbeat.beat field to proto"
```

---

### Task 2: Ollama Client Improvements

**Files:**
- Modify: `server/src/chat_server/ollama.py`
- Modify: `server/tests/test_ollama.py`

- [ ] **Step 1: Write test for usage stats capture**

Add to `server/tests/test_ollama.py`:

```python
@pytest.mark.asyncio
async def test_chat_returns_usage_stats(client):
    """The final done:true chunk should yield usage stats."""
    lines = [
        json.dumps({"message": {"content": "Hi"}, "done": False}),
        json.dumps({
            "message": {"content": ""},
            "done": True,
            "prompt_eval_count": 11,
            "eval_count": 5,
        }),
    ]
    mock_response = AsyncMock()
    mock_response.raise_for_status = MagicMock()

    async def fake_aiter_lines():
        for line in lines:
            yield line

    mock_response.aiter_lines = fake_aiter_lines
    mock_response.__aenter__ = AsyncMock(return_value=mock_response)
    mock_response.__aexit__ = AsyncMock(return_value=False)

    with patch.object(client._client, "stream", return_value=mock_response):
        tokens = []
        async for token in client.chat("test"):
            tokens.append(token)

    assert tokens == ["Hi"]
    assert client.last_usage == {"prompt_eval_count": 11, "eval_count": 5}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd server && PYTHONPATH=../gen/python uv run pytest tests/test_ollama.py::test_chat_returns_usage_stats -v
```

Expected: FAIL — `AttributeError: 'OllamaClient' object has no attribute 'last_usage'`

- [ ] **Step 3: Implement usage stats, context length, and heartbeat word**

Replace `server/src/chat_server/ollama.py` entirely:

```python
import json
from dataclasses import dataclass, field
from typing import AsyncIterator

import httpx


@dataclass
class UsageStats:
    prompt_tokens: int = 0
    completion_tokens: int = 0


class OllamaClient:
    def __init__(self, base_url: str = "http://localhost:11434", model: str = "qwen3:0.6b"):
        self._model = model
        self._client = httpx.AsyncClient(
            base_url=base_url, timeout=120.0, headers={"Host": "localhost"}
        )
        self.last_usage: dict = {}
        self._context_length: int | None = None

    async def chat(self, message: str, conversation_history: list[dict] | None = None) -> AsyncIterator[str]:
        """Stream chat tokens from Ollama. After iteration, self.last_usage is set."""
        messages = list(conversation_history or [])
        messages.append({"role": "user", "content": message})
        self.last_usage = {}

        async with self._client.stream(
            "POST",
            "/api/chat",
            json={
                "model": self._model,
                "messages": messages,
                "stream": True,
            },
        ) as response:
            response.raise_for_status()
            async for line in response.aiter_lines():
                if not line:
                    continue
                data = json.loads(line)
                if "message" in data and "content" in data["message"]:
                    content = data["message"]["content"]
                    if content:
                        yield content
                if data.get("done"):
                    self.last_usage = {
                        "prompt_eval_count": data.get("prompt_eval_count", 0),
                        "eval_count": data.get("eval_count", 0),
                    }
                    break

    async def get_model_context_length(self) -> int:
        """Get model context window size. Cached after first call."""
        if self._context_length is not None:
            return self._context_length

        response = await self._client.post("/api/show", json={"model": self._model})
        response.raise_for_status()
        data = response.json()
        model_info = data.get("model_info", {})
        for key, value in model_info.items():
            if "context_length" in key:
                self._context_length = int(value)
                return self._context_length
        self._context_length = 0
        return 0

    async def generate_heartbeat_word(self) -> str:
        """Generate a single playful gerund word via Ollama."""
        try:
            response = await self._client.post(
                "/api/generate",
                json={
                    "model": self._model,
                    "prompt": "Output only a single creative playful gerund word like shimmering, gallivanting, or percolating. Only the word, nothing else.",
                    "stream": False,
                },
                timeout=10.0,
            )
            response.raise_for_status()
            data = response.json()
            word = data.get("response", "...").strip().strip(".")
            # Take just the first word in case model outputs more
            return word.split()[0] if word else "..."
        except Exception:
            return "..."

    async def close(self):
        await self._client.aclose()
```

- [ ] **Step 4: Run all tests**

```bash
cd server && PYTHONPATH=../gen/python uv run pytest tests/ -v
```

Expected: all tests pass (existing + new usage stats test).

- [ ] **Step 5: Commit**

```bash
git add server/src/chat_server/ollama.py server/tests/test_ollama.py
git commit -m "feat: ollama client with usage stats, context length, and heartbeat word"
```

---

### Task 3: Server Conversation History, Usage Reporting & Heartbeat

**Files:**
- Modify: `server/src/chat_server/service.py`

- [ ] **Step 1: Rewrite service.py with conversation history, usage, and heartbeat**

Replace `server/src/chat_server/service.py` entirely:

```python
import asyncio
import grpc
from chat.v1 import chat_pb2, chat_pb2_grpc
from chat_server.ollama import OllamaClient


class ChatServiceServicer(chat_pb2_grpc.ChatServiceServicer):
    def __init__(self, ollama_url: str, ollama_model: str):
        self._ollama = OllamaClient(base_url=ollama_url, model=ollama_model)
        self._context_length: int = 0

    async def initialize(self):
        """Call once at startup to cache model info."""
        self._context_length = await self._ollama.get_model_context_length()

    async def SendMessage(self, request, context):
        """Unary RPC: send message, get complete response."""
        try:
            full_response = ""
            async for token in self._ollama.chat(request.text):
                full_response += token
            return chat_pb2.SendMessageResponse(
                conversation_id=request.conversation_id,
                text=full_response,
            )
        except Exception as e:
            await context.abort(grpc.StatusCode.INTERNAL, str(e))

    async def Chat(self, request_iterator, context):
        """Bidirectional streaming RPC with cancel, history, usage, and heartbeat."""
        send_queue: asyncio.Queue = asyncio.Queue()
        cancel_event = asyncio.Event()
        generation_task: asyncio.Task | None = None
        conversation_history: list[dict] = []

        async def heartbeat_loop():
            """Send a playful heartbeat word every 15 seconds."""
            try:
                while True:
                    await asyncio.sleep(15)
                    word = await self._ollama.generate_heartbeat_word()
                    await send_queue.put(
                        chat_pb2.ChatResponse(
                            conversation_id="",
                            heartbeat=chat_pb2.Heartbeat(beat=word),
                        )
                    )
            except asyncio.CancelledError:
                pass

        heartbeat_task = asyncio.create_task(heartbeat_loop())

        async def read_client_messages():
            nonlocal generation_task
            try:
                async for msg in request_iterator:
                    action = msg.WhichOneof("action")

                    if action == "user_message":
                        # Cancel any in-flight generation
                        if generation_task and not generation_task.done():
                            cancel_event.set()
                            generation_task.cancel()
                            try:
                                await generation_task
                            except asyncio.CancelledError:
                                pass

                        cancel_event.clear()
                        conversation_history.append(
                            {"role": "user", "content": msg.user_message.text}
                        )
                        generation_task = asyncio.create_task(
                            self._generate(
                                msg.conversation_id,
                                msg.user_message.text,
                                send_queue,
                                cancel_event,
                                conversation_history,
                            )
                        )

                    elif action == "cancel":
                        cancel_event.set()
                        if generation_task and not generation_task.done():
                            generation_task.cancel()
                            try:
                                await generation_task
                            except asyncio.CancelledError:
                                pass
                        await send_queue.put(
                            chat_pb2.ChatResponse(
                                conversation_id=msg.conversation_id,
                                ack=chat_pb2.Acknowledgement(
                                    acknowledged_type="cancel"
                                ),
                            )
                        )

                    elif action == "add_context":
                        await send_queue.put(
                            chat_pb2.ChatResponse(
                                conversation_id=msg.conversation_id,
                                ack=chat_pb2.Acknowledgement(
                                    acknowledged_type="context_injection"
                                ),
                            )
                        )
            finally:
                await send_queue.put(None)

        reader_task = asyncio.create_task(read_client_messages())

        try:
            while True:
                response = await send_queue.get()
                if response is None:
                    break
                yield response
        finally:
            heartbeat_task.cancel()
            reader_task.cancel()
            if generation_task and not generation_task.done():
                generation_task.cancel()

    async def _generate(
        self,
        conversation_id: str,
        text: str,
        queue: asyncio.Queue,
        cancel_event: asyncio.Event,
        conversation_history: list[dict],
    ):
        """Stream tokens from Ollama into the send queue, then report usage."""
        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_THINKING),
            )
        )
        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_GENERATING),
            )
        )

        accumulated_response = ""
        try:
            async for token_text in self._ollama.chat(
                text, conversation_history=conversation_history[:-1]
            ):
                if cancel_event.is_set():
                    break
                accumulated_response += token_text
                await queue.put(
                    chat_pb2.ChatResponse(
                        conversation_id=conversation_id,
                        token=chat_pb2.Token(text=token_text),
                    )
                )
        except asyncio.CancelledError:
            pass
        except Exception as e:
            await queue.put(
                chat_pb2.ChatResponse(
                    conversation_id=conversation_id,
                    error=chat_pb2.Error(code=13, message=str(e)),
                )
            )

        # Record assistant response in history (even if partial/cancelled)
        if accumulated_response:
            conversation_history.append(
                {"role": "assistant", "content": accumulated_response}
            )

        # Send usage info
        usage = self._ollama.last_usage
        if usage:
            await queue.put(
                chat_pb2.ChatResponse(
                    conversation_id=conversation_id,
                    usage=chat_pb2.UsageInfo(
                        prompt_tokens=usage.get("prompt_eval_count", 0),
                        completion_tokens=usage.get("eval_count", 0),
                        context_length=self._context_length,
                    ),
                )
            )

        await queue.put(
            chat_pb2.ChatResponse(
                conversation_id=conversation_id,
                status=chat_pb2.StatusUpdate(phase=chat_pb2.PHASE_DONE),
            )
        )
```

- [ ] **Step 2: Update main.py to call initialize()**

In `server/src/chat_server/main.py`, after creating the servicer, add:

```python
    servicer = ChatServiceServicer(
        ollama_url=settings.ollama_url,
        ollama_model=settings.ollama_model,
    )
    await servicer.initialize()  # <-- add this line
```

- [ ] **Step 3: Run tests**

```bash
cd server && PYTHONPATH=../gen/python uv run pytest tests/ -v
```

Expected: all tests pass. (Existing service tests mock the ollama client so they still work. The `_generate` signature changed to accept `conversation_history` — update test mocks if needed.)

- [ ] **Step 4: Commit**

```bash
git add server/src/chat_server/service.py server/src/chat_server/main.py
git commit -m "feat: server conversation history, usage reporting, and heartbeat"
```

---

### Task 4: Go TUI Event Log Types & Styles

**Files:**
- Create: `client/tui/events.go`
- Modify: `client/tui/styles.go`

- [ ] **Step 1: Create client/tui/events.go**

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Direction of a gRPC message
type Direction int

const (
	Outgoing Direction = iota // client → server
	Incoming                  // server → client
)

// EventEntry is a single line in the event log
type EventEntry struct {
	Dir     Direction
	Type    string // e.g. "UserMessage", "Token", "StatusUpdate"
	Payload string // compact payload summary
}

var (
	// Outgoing styles
	OutgoingUserMsgStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("10")) // green
	OutgoingCancelStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("11")) // yellow
	OutgoingContextStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("14")) // cyan

	// Incoming styles
	IncomingTokenStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Faint(true) // dim white
	IncomingStatusStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))              // magenta
	IncomingErrorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)     // red bold
	IncomingHeartbeatStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Faint(true)   // dim yellow
	IncomingAckStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))               // cyan
	IncomingUsageStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))               // blue

	ArrowOutStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	ArrowInStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
)

func (e EventEntry) Render() string {
	var arrow, styled string

	if e.Dir == Outgoing {
		arrow = ArrowOutStyle.Render("→")
	} else {
		arrow = ArrowInStyle.Render("←")
	}

	style := e.styleForType()
	if e.Payload != "" {
		styled = style.Render(fmt.Sprintf("%s %s", e.Type, e.Payload))
	} else {
		styled = style.Render(e.Type)
	}

	return fmt.Sprintf("%s %s", arrow, styled)
}

func (e EventEntry) styleForType() lipgloss.Style {
	if e.Dir == Outgoing {
		switch e.Type {
		case "UserMessage":
			return OutgoingUserMsgStyle
		case "CancelGeneration":
			return OutgoingCancelStyle
		case "ContextInjection":
			return OutgoingContextStyle
		}
		return OutgoingUserMsgStyle
	}

	switch e.Type {
	case "Token":
		return IncomingTokenStyle
	case "StatusUpdate":
		return IncomingStatusStyle
	case "Error":
		return IncomingErrorStyle
	case "Heartbeat":
		return IncomingHeartbeatStyle
	case "Acknowledgement":
		return IncomingAckStyle
	case "UsageInfo":
		return IncomingUsageStyle
	}
	return IncomingTokenStyle
}

// Truncate helper for token text in event log
func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
```

- [ ] **Step 2: Update client/tui/styles.go**

Add a panel border style. Replace the entire file:

```go
package tui

import "github.com/charmbracelet/lipgloss"

var (
	UserStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).
			Bold(true)

	AssistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12"))

	StatusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236")).
			Foreground(lipgloss.Color("248")).
			Padding(0, 1)

	ErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("9")).
			Bold(true)

	InputPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("205"))

	PanelBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.NormalBorder()).
				BorderForeground(lipgloss.Color("240"))

	PanelTitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("248")).
			Bold(true)
)
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./client/...
```

Expected: compiles (events.go and styles.go are standalone, not yet used by model.go).

- [ ] **Step 4: Commit**

```bash
git add client/tui/events.go client/tui/styles.go
git commit -m "feat: event log types and color styles for TUI"
```

---

### Task 5: Go TUI Split-Screen, Event Logging & Mid-Gen Interrupt

**Files:**
- Modify: `client/tui/commands.go`
- Modify: `client/tui/model.go`

- [ ] **Step 1: Update commands.go with UsageMsg and event log entries**

Replace `client/tui/commands.go` entirely:

```go
package tui

import (
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
	"github.com/michal-derdak/chat/client/grpcclient"
)

// Bubble Tea messages
type TokenMsg struct{ Text string }
type StatusMsg struct{ Phase string }
type ErrorMsg struct{ Err error }
type StreamEndMsg struct{}
type AckMsg struct{ Type string }
type HeartbeatMsg struct{ Beat string }
type UsageMsg struct {
	PromptTokens    int
	CompletionTokens int
	ContextLength   int
}

// EventLogMsg carries an event entry to be appended to the log
type EventLogMsg struct{ Entry EventEntry }

// WaitForEvent returns a command that blocks on the next server event.
func WaitForEvent(sc *grpcclient.StreamClient) tea.Cmd {
	return func() tea.Msg {
		resp, err := sc.Recv()
		if err != nil {
			if sc.IsEOF(err) {
				return StreamEndMsg{}
			}
			return ErrorMsg{Err: fmt.Errorf("recv: %w", err)}
		}

		switch evt := resp.Event.(type) {
		case *chatv1.ChatResponse_Token:
			return TokenMsg{Text: evt.Token.GetText()}
		case *chatv1.ChatResponse_Status:
			return StatusMsg{Phase: evt.Status.GetPhase().String()}
		case *chatv1.ChatResponse_Error:
			return ErrorMsg{Err: fmt.Errorf("server: %s", evt.Error.GetMessage())}
		case *chatv1.ChatResponse_Ack:
			return AckMsg{Type: evt.Ack.GetAcknowledgedType()}
		case *chatv1.ChatResponse_Heartbeat:
			return HeartbeatMsg{Beat: evt.Heartbeat.GetBeat()}
		case *chatv1.ChatResponse_Usage:
			return UsageMsg{
				PromptTokens:    int(evt.Usage.GetPromptTokens()),
				CompletionTokens: int(evt.Usage.GetCompletionTokens()),
				ContextLength:   int(evt.Usage.GetContextLength()),
			}
		default:
			return nil
		}
	}
}

// SendMessage sends a user message on the bidi stream.
func SendMessage(sc *grpcclient.StreamClient, conversationID, text string) tea.Cmd {
	return func() tea.Msg {
		err := sc.Send(&chatv1.ChatRequest{
			ConversationId: conversationID,
			Action: &chatv1.ChatRequest_UserMessage{
				UserMessage: &chatv1.UserMessage{Text: text},
			},
		})
		if err != nil && err != io.EOF {
			return ErrorMsg{Err: fmt.Errorf("send: %w", err)}
		}
		return EventLogMsg{Entry: EventEntry{
			Dir:     Outgoing,
			Type:    "UserMessage",
			Payload: fmt.Sprintf("%q", truncate(text, 30)),
		}}
	}
}

// SendCancel sends a cancel signal on the bidi stream.
func SendCancel(sc *grpcclient.StreamClient, conversationID string) tea.Cmd {
	return func() tea.Msg {
		err := sc.Send(&chatv1.ChatRequest{
			ConversationId: conversationID,
			Action: &chatv1.ChatRequest_Cancel{
				Cancel: &chatv1.CancelGeneration{},
			},
		})
		if err != nil && err != io.EOF {
			return ErrorMsg{Err: fmt.Errorf("send cancel: %w", err)}
		}
		return EventLogMsg{Entry: EventEntry{
			Dir:  Outgoing,
			Type: "CancelGeneration",
		}}
	}
}
```

- [ ] **Step 2: Rewrite model.go with split-screen, event log, and mid-gen interrupt**

Replace `client/tui/model.go` entirely:

```go
package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/michal-derdak/chat/client/grpcclient"
)

type ChatMessage struct {
	Role    string
	Content string
}

type Model struct {
	// Chat panel (left)
	chatViewport viewport.Model
	messages     []ChatMessage
	input        textinput.Model

	// Event log panel (right)
	eventViewport viewport.Model
	eventLog      []EventEntry

	// Stream state
	stream         *grpcclient.StreamClient
	conversationID string
	streaming      bool
	status         string
	err            error

	// Usage tracking
	promptTokens     int
	completionTokens int
	contextLength    int

	// Layout
	ready  bool
	width  int
	height int
}

func NewModel(stream *grpcclient.StreamClient, conversationID string) Model {
	ti := textinput.New()
	ti.Placeholder = "Type a message... (Enter to send, Esc to quit)"
	ti.Focus()
	ti.Width = 80

	return Model{
		input:          ti,
		messages:       []ChatMessage{},
		eventLog:       []EventEntry{},
		status:         "connected",
		stream:         stream,
		conversationID: conversationID,
	}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m.handleResize(), nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case TokenMsg:
		m.addEvent(EventEntry{Dir: Incoming, Type: "Token", Payload: fmt.Sprintf("%q", truncate(msg.Text, 30))})
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.Role == "assistant" {
				last.Content += msg.Text
			}
		}
		m.refreshPanels()
		return m, WaitForEvent(m.stream)

	case StatusMsg:
		m.addEvent(EventEntry{Dir: Incoming, Type: "StatusUpdate", Payload: msg.Phase})
		m.status = msg.Phase
		if msg.Phase == "PHASE_DONE" {
			m.streaming = false
			m.status = "ready"
		}
		m.refreshPanels()
		return m, WaitForEvent(m.stream)

	case AckMsg:
		m.addEvent(EventEntry{Dir: Incoming, Type: "Acknowledgement", Payload: msg.Type})
		m.status = fmt.Sprintf("ack: %s", msg.Type)
		m.refreshPanels()
		return m, WaitForEvent(m.stream)

	case HeartbeatMsg:
		m.addEvent(EventEntry{Dir: Incoming, Type: "Heartbeat", Payload: fmt.Sprintf("%q", msg.Beat)})
		m.refreshPanels()
		return m, WaitForEvent(m.stream)

	case UsageMsg:
		m.promptTokens = msg.PromptTokens
		m.completionTokens = msg.CompletionTokens
		m.contextLength = msg.ContextLength
		totalUsed := msg.PromptTokens + msg.CompletionTokens
		m.addEvent(EventEntry{
			Dir:     Incoming,
			Type:    "UsageInfo",
			Payload: fmt.Sprintf("%d+%d/%d", msg.PromptTokens, msg.CompletionTokens, msg.ContextLength),
		})
		m.refreshPanels()
		return m, WaitForEvent(m.stream)

	case ErrorMsg:
		m.addEvent(EventEntry{Dir: Incoming, Type: "Error", Payload: msg.Err.Error()})
		m.err = msg.Err
		m.streaming = false
		m.status = fmt.Sprintf("error: %s", msg.Err)
		m.refreshPanels()
		return m, nil

	case StreamEndMsg:
		m.streaming = false
		m.status = "stream ended"
		m.refreshPanels()
		return m, nil

	case EventLogMsg:
		m.addEvent(msg.Entry)
		m.refreshPanels()
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) handleResize() Model {
	statusHeight := 1
	inputHeight := 1
	chrome := statusHeight + inputHeight + 4 // borders + gaps
	panelHeight := m.height - chrome
	if panelHeight < 3 {
		panelHeight = 3
	}
	panelWidth := m.width/2 - 2

	if !m.ready {
		m.chatViewport = viewport.New(panelWidth, panelHeight)
		m.eventViewport = viewport.New(panelWidth, panelHeight)
		m.ready = true
	} else {
		m.chatViewport.Width = panelWidth
		m.chatViewport.Height = panelHeight
		m.eventViewport.Width = panelWidth
		m.eventViewport.Height = panelHeight
	}
	m.input.Width = m.width - 4
	m.refreshPanels()
	return *m
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c":
		if m.streaming {
			m.status = "cancelling..."
			return m, SendCancel(m.stream, m.conversationID)
		}
		return m, tea.Quit
	case "esc":
		m.stream.Close()
		return m, tea.Quit
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		m.input.Reset()

		var cmds []tea.Cmd

		// If streaming, cancel current generation first
		if m.streaming {
			// Mark current assistant message as cancelled
			if len(m.messages) > 0 {
				last := &m.messages[len(m.messages)-1]
				if last.Role == "assistant" {
					last.Content += " [cancelled]"
				}
			}
			cmds = append(cmds, SendCancel(m.stream, m.conversationID))
		}

		// Add new user message + empty assistant slot
		m.messages = append(m.messages, ChatMessage{Role: "user", Content: text})
		m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: ""})
		m.streaming = true
		m.status = "sending..."
		m.refreshPanels()

		cmds = append(cmds,
			SendMessage(m.stream, m.conversationID, text),
			WaitForEvent(m.stream),
		)
		return m, tea.Batch(cmds...)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *Model) addEvent(e EventEntry) {
	m.eventLog = append(m.eventLog, e)
}

func (m *Model) refreshPanels() {
	m.chatViewport.SetContent(m.renderMessages())
	m.chatViewport.GotoBottom()
	m.eventViewport.SetContent(m.renderEventLog())
	m.eventViewport.GotoBottom()
}

func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	halfWidth := m.width / 2

	// Chat panel
	chatTitle := PanelTitleStyle.Render(" Chat ")
	chatPanel := PanelBorderStyle.Width(halfWidth - 2).Height(m.chatViewport.Height + 1).Render(
		chatTitle + "\n" + m.chatViewport.View(),
	)

	// Event log panel
	eventTitle := PanelTitleStyle.Render(" gRPC Event Log ")
	eventPanel := PanelBorderStyle.Width(halfWidth - 2).Height(m.eventViewport.Height + 1).Render(
		eventTitle + "\n" + m.eventViewport.View(),
	)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, chatPanel, eventPanel)

	// Status bar
	usageStr := ""
	if m.contextLength > 0 {
		total := m.promptTokens + m.completionTokens
		pct := float64(total) / float64(m.contextLength) * 100
		usageStr = fmt.Sprintf("tokens: %d/%d (%.1f%%) | ", total, m.contextLength, pct)
	}
	statusBar := StatusBarStyle.Width(m.width).Render(
		fmt.Sprintf(" %s%s | streaming: %v", usageStr, m.status, m.streaming),
	)

	return fmt.Sprintf("%s\n%s\n%s", panels, statusBar, m.input.View())
}

func (m Model) renderMessages() string {
	var b strings.Builder
	for i, msg := range m.messages {
		switch msg.Role {
		case "user":
			b.WriteString(UserStyle.Render("You: "))
			b.WriteString(msg.Content)
		case "assistant":
			b.WriteString(AssistantStyle.Render("AI: "))
			b.WriteString(msg.Content)
			if m.streaming && i == len(m.messages)-1 {
				b.WriteString("▌")
			}
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

func (m Model) renderEventLog() string {
	var b strings.Builder
	for _, e := range m.eventLog {
		b.WriteString(e.Render())
		b.WriteString("\n")
	}
	return b.String()
}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./client/...
```

Expected: compiles without errors.

- [ ] **Step 4: Commit**

```bash
git add client/tui/commands.go client/tui/model.go
git commit -m "feat: split-screen TUI with event log, mid-gen interrupt, and usage display"
```

---

### Task 6: Rebuild, Deploy & Verify

**Files:** None (deployment only)

- [ ] **Step 1: Rebuild and redeploy**

```bash
make deploy-all
```

- [ ] **Step 2: Test with grpcurl**

```bash
grpcurl -plaintext -H 'authorization: Bearer demo-token' -d '{"conversation_id":"test","text":"Say hello"}' localhost:50051 chat.v1.ChatService/SendMessage
```

Expected: returns response with text.

- [ ] **Step 3: Test TUI client**

```bash
make client
```

Expected:
- Split-screen layout: chat on left, event log on right
- Type a message → see `→ UserMessage` in event log, then `← StatusUpdate THINKING`, `← Token` events streaming
- Every 15s a `← Heartbeat "word"` appears in the event log
- After generation: `← UsageInfo` with token counts, status bar shows context usage
- Type a new message mid-generation → see `→ CancelGeneration`, `← Acknowledgement "cancel"`, then new `→ UserMessage` in the event log
- AI responses are contextual (knows chat history)

- [ ] **Step 4: Commit any fixes**

If fixes are needed, commit them with descriptive messages.

---

## Verification Checklist

After all tasks:

1. Proto: `buf lint` passes
2. Python tests: `cd server && PYTHONPATH=../gen/python uv run pytest tests/ -v` all pass
3. Go build: `go build ./client/... ./gateway/...` compiles
4. TUI: split-screen layout renders correctly
5. Event log: all 9 event types appear with correct colors
6. Mid-gen interrupt: Enter during streaming cancels and starts new generation
7. Conversation history: AI remembers previous messages in the session
8. Usage: status bar shows token count after each response
9. Heartbeat: playful words appear every 15s in event log
