# Unified TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge four separate client modes into a single TUI with tab switching, sharing two gRPC connections via HTTP/2 multiplexing.

**Architecture:** Two `grpc.ClientConn` (plaintext + mTLS) created at startup. A `ModeState` struct holds per-mode state (messages, events, stream). `UnifiedModel` wraps mode-specific commands with a `ModeMsg` envelope so async responses route to the correct tab. Tab key cycles modes.

**Tech Stack:** Go Bubble Tea, Lipgloss, gRPC

---

## File Structure

```
New:
  client/tui/unified_model.go   — UnifiedModel with 4 ModeStates, tab bar, message routing

Modified:
  client/tui/styles.go          — tab bar styles
  client/main.go                — dual connections, new flags, single model
  Makefile                       — single client target

Removed:
  client/tui/model.go           — replaced by unified_model.go
  client/tui/unary_model.go     — merged into unified_model.go

Unchanged:
  client/tui/commands.go         — existing bidi commands
  client/tui/unary_commands.go   — existing unary commands
  client/tui/events.go           — event types and rendering
  client/grpcclient/             — connection and stream code
```

---

### Task 1: Add Tab Bar Styles

**Files:**
- Modify: `client/tui/styles.go`

- [ ] **Step 1: Add tab styles to styles.go**

Read `client/tui/styles.go` and add these styles to the `var` block:

```go
	TabActiveStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("15")).
			Background(lipgloss.Color("62")).
			Padding(0, 2)

	TabInactiveStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Background(lipgloss.Color("236")).
			Padding(0, 2)

	TabBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("236"))
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./client/...
```

- [ ] **Step 3: Commit**

```bash
git add client/tui/styles.go
git commit -m "feat: add tab bar styles for unified TUI"
```

---

### Task 2: Create Unified Model

**Files:**
- Create: `client/tui/unified_model.go`

This is the main task. The unified model has:
- 4 `ModeState` structs (one per tab)
- `ModeMsg` wrapper for routing async messages to the correct tab
- `withMode()` helper that wraps any `tea.Cmd` to tag its result with a mode index
- Tab key handling
- Delegates to mode-specific logic based on whether the mode is unary or bidi

- [ ] **Step 1: Create client/tui/unified_model.go**

```go
package tui

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
	"github.com/michal-derdak/chat/client/grpcclient"
)

// ModeMsg wraps a tea.Msg with the mode index it belongs to
type ModeMsg struct {
	ModeIndex int
	Inner     tea.Msg
}

// withMode wraps a tea.Cmd so its result is tagged with modeIndex
func withMode(modeIndex int, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		msg := cmd()
		if msg == nil {
			return nil
		}
		return ModeMsg{ModeIndex: modeIndex, Inner: msg}
	}
}

// ModeState holds per-tab state
type ModeState struct {
	Name           string
	Client         chatv1.ChatServiceClient
	IsStream       bool
	ConnErr        error // non-nil if connection failed at startup

	Messages       []ChatMessage
	EventLog       []EventEntry
	Stream         *grpcclient.StreamClient
	ConversationID string
	PodName        string
	Streaming      bool // bidi: actively generating
	Waiting        bool // unary: waiting for response
	Reconnecting   bool
	Timeout        time.Duration

	PromptTokens     int
	CompletionTokens int
	ContextLength    int
}

func newModeState(name string, client chatv1.ChatServiceClient, connErr error, isStream bool, timeout time.Duration) *ModeState {
	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	return &ModeState{
		Name:           name,
		Client:         client,
		IsStream:       isStream,
		ConnErr:        connErr,
		Messages:       []ChatMessage{},
		EventLog:       []EventEntry{},
		ConversationID: "chat-" + hex.EncodeToString(idBytes),
		Timeout:        timeout,
	}
}

type UnifiedModel struct {
	modes      [4]*ModeState
	activeMode int

	chatViewport  viewport.Model
	eventViewport viewport.Model
	input         textinput.Model

	ready  bool
	width  int
	height int
}

func NewUnifiedModel(
	grpcClient chatv1.ChatServiceClient, grpcErr error,
	envoyClient chatv1.ChatServiceClient, envoyErr error,
	timeout time.Duration,
) UnifiedModel {
	ti := textinput.New()
	ti.Placeholder = "Type a message... (Tab to switch mode, Esc to quit)"
	ti.Focus()
	ti.Width = 80

	return UnifiedModel{
		modes: [4]*ModeState{
			newModeState("gRPC Unary", grpcClient, grpcErr, false, timeout),
			newModeState("gRPC Stream", grpcClient, grpcErr, true, timeout),
			newModeState("Envoy Unary", envoyClient, envoyErr, false, timeout),
			newModeState("Envoy Stream", envoyClient, envoyErr, true, timeout),
		},
		activeMode: 0,
		input:      ti,
	}
}

func (m UnifiedModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m UnifiedModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m.handleResize(), nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case ModeMsg:
		return m.handleModeMsg(msg)
	}

	// Pass to text input
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m UnifiedModel) handleModeMsg(msg ModeMsg) (tea.Model, tea.Cmd) {
	idx := msg.ModeIndex
	if idx < 0 || idx >= 4 {
		return m, nil
	}
	mode := m.modes[idx]

	switch inner := msg.Inner.(type) {
	// --- Bidi stream messages ---
	case TokenMsg:
		if len(mode.Messages) > 0 {
			last := &mode.Messages[len(mode.Messages)-1]
			if last.Role == "assistant" {
				last.Content += inner.Text
			}
		}
		m.refreshPanels()
		return m, withMode(idx, WaitForEvent(mode.Stream))

	case StatusMsg:
		mode.EventLog = append(mode.EventLog, EventEntry{Dir: Incoming, Type: "StatusUpdate", Payload: inner.Phase})
		if inner.Phase == "PHASE_DONE" {
			mode.Streaming = false
		}
		m.refreshPanels()
		return m, withMode(idx, WaitForEvent(mode.Stream))

	case AckMsg:
		mode.EventLog = append(mode.EventLog, EventEntry{Dir: Incoming, Type: "Acknowledgement", Payload: inner.Type})
		m.refreshPanels()
		return m, withMode(idx, WaitForEvent(mode.Stream))

	case HeartbeatMsg:
		mode.EventLog = append(mode.EventLog, EventEntry{Dir: Incoming, Type: "Heartbeat", Payload: fmt.Sprintf("%q", inner.Beat)})
		m.refreshPanels()
		return m, withMode(idx, WaitForEvent(mode.Stream))

	case UsageMsg:
		mode.PromptTokens = inner.PromptTokens
		mode.CompletionTokens = inner.CompletionTokens
		mode.ContextLength = inner.ContextLength
		mode.EventLog = append(mode.EventLog, EventEntry{
			Dir: Incoming, Type: "UsageInfo",
			Payload: fmt.Sprintf("%d+%d/%d", inner.PromptTokens, inner.CompletionTokens, inner.ContextLength),
		})
		m.refreshPanels()
		return m, withMode(idx, WaitForEvent(mode.Stream))

	case ShutdownMsg:
		mode.EventLog = append(mode.EventLog, EventEntry{Dir: Incoming, Type: "ServerShutdown", Payload: fmt.Sprintf("%q", inner.Reason)})
		mode.Messages = append(mode.Messages, ChatMessage{Role: "system", Content: "[Server restarting, reconnecting...]"})
		mode.Streaming = false
		mode.Reconnecting = true
		m.refreshPanels()
		return m, withMode(idx, m.reconnectCmd(idx))

	case ReconnectedMsg:
		mode.Stream = inner.Stream
		mode.Reconnecting = false
		mode.PodName = mode.Stream.PodName()
		mode.EventLog = append(mode.EventLog, EventEntry{Dir: Outgoing, Type: "Reconnected", Payload: mode.PodName})
		m.refreshPanels()
		return m, withMode(idx, WaitForEvent(mode.Stream))

	// --- Unary messages ---
	case UnaryResponseMsg:
		mode.EventLog = append(mode.EventLog, EventEntry{
			Dir: Incoming, Type: "Response",
			Payload: fmt.Sprintf("(%s) %.1fs", inner.PodName, inner.Duration.Seconds()),
		})
		if len(mode.Messages) > 0 {
			last := &mode.Messages[len(mode.Messages)-1]
			if last.Role == "assistant" {
				last.Content = inner.Text
			}
		}
		mode.Waiting = false
		mode.PodName = inner.PodName
		m.refreshPanels()
		return m, nil

	// --- Shared messages ---
	case ErrorMsg:
		if mode.Reconnecting {
			return m, nil
		}
		mode.EventLog = append(mode.EventLog, EventEntry{Dir: Incoming, Type: "Error", Payload: inner.Err.Error()})
		mode.Streaming = false
		mode.Waiting = false
		if mode.IsStream && (strings.Contains(inner.Err.Error(), "Unavailable") || strings.Contains(inner.Err.Error(), "EOF") || strings.Contains(inner.Err.Error(), "transport is closing")) {
			mode.Messages = append(mode.Messages, ChatMessage{Role: "system", Content: "[Connection lost, reconnecting...]"})
			mode.Reconnecting = true
			m.refreshPanels()
			return m, withMode(idx, m.reconnectCmd(idx))
		}
		m.refreshPanels()
		return m, nil

	case StreamEndMsg:
		mode.Streaming = false
		m.refreshPanels()
		return m, nil

	case EventLogMsg:
		mode.EventLog = append(mode.EventLog, inner.Entry)
		m.refreshPanels()
		return m, nil
	}

	return m, nil
}

func (m *UnifiedModel) handleResize() UnifiedModel {
	tabHeight := 1
	statusHeight := 1
	inputHeight := 1
	chrome := tabHeight + statusHeight + inputHeight + 5
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

func (m *UnifiedModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	mode := m.modes[m.activeMode]

	switch msg.String() {
	case "tab":
		m.activeMode = (m.activeMode + 1) % 4
		m.refreshPanels()
		return m, nil

	case "ctrl+c":
		if mode.IsStream && mode.Streaming {
			return m, withMode(m.activeMode, SendCancel(mode.Stream, mode.ConversationID))
		}
		return m, tea.Quit

	case "esc":
		// Close all active streams
		for _, ms := range m.modes {
			if ms.Stream != nil {
				ms.Stream.Close()
			}
		}
		return m, tea.Quit

	case "enter":
		if mode.ConnErr != nil {
			return m, nil // can't send on a failed connection
		}
		text := strings.TrimSpace(m.input.Value())
		if text == "" {
			return m, nil
		}
		if mode.IsStream {
			return m.handleStreamEnter(text)
		}
		return m.handleUnaryEnter(text)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *UnifiedModel) handleStreamEnter(text string) (tea.Model, tea.Cmd) {
	idx := m.activeMode
	mode := m.modes[idx]

	if mode.Waiting {
		return m, nil
	}
	m.input.Reset()

	var cmds []tea.Cmd

	// Open stream lazily on first message
	if mode.Stream == nil {
		stream, err := grpcclient.OpenStream(mode.Client, mode.Timeout)
		if err != nil {
			mode.EventLog = append(mode.EventLog, EventEntry{Dir: Incoming, Type: "Error", Payload: err.Error()})
			m.refreshPanels()
			return m, nil
		}
		mode.Stream = stream
		mode.PodName = stream.PodName()
		mode.EventLog = append(mode.EventLog, EventEntry{Dir: Incoming, Type: "Connected", Payload: mode.PodName})
	}

	// Cancel in-flight generation
	if mode.Streaming {
		if len(mode.Messages) > 0 {
			last := &mode.Messages[len(mode.Messages)-1]
			if last.Role == "assistant" {
				last.Content += " [cancelled]"
			}
		}
		cmds = append(cmds, withMode(idx, SendCancel(mode.Stream, mode.ConversationID)))
	}

	mode.Messages = append(mode.Messages, ChatMessage{Role: "user", Content: text})
	mode.Messages = append(mode.Messages, ChatMessage{Role: "assistant", Content: ""})
	mode.Streaming = true
	m.refreshPanels()

	cmds = append(cmds,
		withMode(idx, SendMessage(mode.Stream, mode.ConversationID, text)),
		withMode(idx, WaitForEvent(mode.Stream)),
	)
	return m, tea.Batch(cmds...)
}

func (m *UnifiedModel) handleUnaryEnter(text string) (tea.Model, tea.Cmd) {
	idx := m.activeMode
	mode := m.modes[idx]

	if mode.Waiting {
		return m, nil
	}
	m.input.Reset()

	mode.EventLog = append(mode.EventLog, EventEntry{
		Dir: Outgoing, Type: "SendMessage",
		Payload: fmt.Sprintf("%q", truncate(text, 30)),
	})
	mode.Messages = append(mode.Messages, ChatMessage{Role: "user", Content: text})
	mode.Messages = append(mode.Messages, ChatMessage{Role: "assistant", Content: "..."})
	mode.Waiting = true
	m.refreshPanels()

	return m, withMode(idx, SendUnary(mode.Client, mode.ConversationID, text))
}

func (m UnifiedModel) reconnectCmd(modeIdx int) tea.Cmd {
	mode := m.modes[modeIdx]
	oldStream := mode.Stream
	client := mode.Client
	timeout := mode.Timeout
	return func() tea.Msg {
		if oldStream != nil {
			oldStream.Close()
		}
		newStream, err := grpcclient.OpenStream(client, timeout)
		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("reconnect failed: %w", err)}
		}
		return ReconnectedMsg{Stream: newStream}
	}
}

func (m *UnifiedModel) refreshPanels() {
	mode := m.modes[m.activeMode]
	m.chatViewport.SetContent(m.renderMessages(mode))
	m.chatViewport.GotoBottom()
	m.eventViewport.SetContent(m.renderEventLog(mode))
	m.eventViewport.GotoBottom()
}

func (m UnifiedModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	mode := m.modes[m.activeMode]
	halfWidth := m.width / 2

	// Tab bar
	tabBar := m.renderTabBar()

	// Chat panel
	chatTitle := PanelTitleStyle.Render(fmt.Sprintf(" Chat (%s) ", mode.Name))
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
	statusBar := StatusBarStyle.Width(m.width).Render(m.renderStatus(mode))

	return fmt.Sprintf("%s\n%s\n%s\n%s", tabBar, panels, statusBar, m.input.View())
}

func (m UnifiedModel) renderTabBar() string {
	var tabs []string
	for i, mode := range m.modes {
		name := mode.Name
		if i == m.activeMode {
			tabs = append(tabs, TabActiveStyle.Render(name))
		} else {
			tabs = append(tabs, TabInactiveStyle.Render(name))
		}
	}
	bar := lipgloss.JoinHorizontal(lipgloss.Bottom, tabs...)
	return TabBarStyle.Width(m.width).Render(bar)
}

func (m UnifiedModel) renderStatus(mode *ModeState) string {
	if mode.ConnErr != nil {
		return fmt.Sprintf(" %s | connection failed: %s", mode.Name, mode.ConnErr)
	}

	parts := []string{" " + mode.Name}

	if mode.PodName != "" {
		parts = append(parts, fmt.Sprintf("pod: %s", mode.PodName))
	}

	if mode.ContextLength > 0 {
		total := mode.PromptTokens + mode.CompletionTokens
		pct := float64(total) / float64(mode.ContextLength) * 100
		parts = append(parts, fmt.Sprintf("tokens: %d/%d (%.1f%%)", total, mode.ContextLength, pct))
	}

	if mode.IsStream {
		if mode.Streaming {
			parts = append(parts, "streaming")
		} else if mode.Reconnecting {
			parts = append(parts, "reconnecting...")
		}
	} else if mode.Waiting {
		parts = append(parts, "waiting...")
	}

	return strings.Join(parts, " | ")
}

func (m UnifiedModel) renderMessages(mode *ModeState) string {
	wrapWidth := m.chatViewport.Width - 4
	if wrapWidth < 10 {
		wrapWidth = 40
	}

	if mode.ConnErr != nil {
		return ErrorStyle.Render(fmt.Sprintf("Connection failed: %s\n\nThis mode is unavailable.", mode.ConnErr))
	}

	var b strings.Builder
	for i, msg := range mode.Messages {
		var prefix, content string
		switch msg.Role {
		case "user":
			prefix = UserStyle.Render("You: ")
			content = msg.Content
		case "assistant":
			prefix = AssistantStyle.Render("AI: ")
			content = msg.Content
			if mode.IsStream && mode.Streaming && i == len(mode.Messages)-1 {
				content += "▌"
			}
		case "system":
			prefix = ErrorStyle.Render("")
			content = ErrorStyle.Render(msg.Content)
		}
		b.WriteString(prefix)
		b.WriteString(wordWrap(content, wrapWidth-6))
		b.WriteString("\n\n")
	}
	return b.String()
}

func (m UnifiedModel) renderEventLog(mode *ModeState) string {
	wrapWidth := m.eventViewport.Width - 4
	if wrapWidth < 10 {
		wrapWidth = 40
	}
	var b strings.Builder
	for _, e := range mode.EventLog {
		b.WriteString(wordWrap(e.Render(), wrapWidth))
		b.WriteString("\n")
	}
	return b.String()
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./client/...
```

This will fail because `model.go` and `unary_model.go` still exist and define conflicting types. We'll remove them in Task 4. For now, verify the file has no syntax errors by temporarily renaming the old files:

```bash
mv client/tui/model.go client/tui/model.go.bak
mv client/tui/unary_model.go client/tui/unary_model.go.bak
go build ./client/...
mv client/tui/model.go.bak client/tui/model.go
mv client/tui/unary_model.go.bak client/tui/unary_model.go
```

- [ ] **Step 3: Commit**

```bash
git add client/tui/unified_model.go
git commit -m "feat: unified TUI model with tab switching and 4 modes"
```

---

### Task 3: Update main.go

**Files:**
- Modify: `client/main.go`

- [ ] **Step 1: Replace client/main.go entirely**

```go
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/michal-derdak/chat/client/grpcclient"
	"github.com/michal-derdak/chat/client/tui"
)

func main() {
	grpcTarget := flag.String("grpc-target", "localhost:50051", "gRPC server address (plaintext)")
	envoyTarget := flag.String("envoy-target", "localhost:50052", "Envoy proxy address (mTLS)")
	token := flag.String("token", "demo-token", "Bearer token for auth")
	caCert := flag.String("ca-cert", "", "Path to CA certificate (for Envoy mTLS)")
	clientCert := flag.String("client-cert", "", "Path to client certificate (for Envoy mTLS)")
	clientKey := flag.String("client-key", "", "Path to client key (for Envoy mTLS)")
	timeout := flag.Duration("timeout", 30*time.Minute, "Stream timeout")
	flag.Parse()

	logFile, err := os.OpenFile("client.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	// Connection 1: plaintext gRPC
	grpcClient, grpcConn, grpcErr := grpcclient.NewChatClient(grpcclient.Config{
		Target:  *grpcTarget,
		Token:   *token,
		UseTLS:  false,
		Timeout: *timeout,
	})
	if grpcConn != nil {
		defer grpcConn.Close()
	}

	// Connection 2: mTLS Envoy
	envoyClient, envoyConn, envoyErr := grpcclient.NewChatClient(grpcclient.Config{
		Target:     *envoyTarget,
		Token:      *token,
		UseTLS:     true,
		CACert:     *caCert,
		ClientCert: *clientCert,
		ClientKey:  *clientKey,
		Timeout:    *timeout,
	})
	if envoyConn != nil {
		defer envoyConn.Close()
	}

	model := tui.NewUnifiedModel(grpcClient, grpcErr, envoyClient, envoyErr, *timeout)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Commit (don't build yet — old model files still exist)**

```bash
git add client/main.go
git commit -m "feat: main.go with dual gRPC connections for unified TUI"
```

---

### Task 4: Remove Old Models, Update Makefile

**Files:**
- Remove: `client/tui/model.go`
- Remove: `client/tui/unary_model.go`
- Modify: `Makefile`

- [ ] **Step 1: Remove old model files**

```bash
rm client/tui/model.go client/tui/unary_model.go
```

- [ ] **Step 2: Update Makefile**

Replace the client targets section. Read the Makefile, find the `# --- Client ---` section, and replace everything from `client:` through `unary-envoy:` (and its continuation lines) with:

```makefile
client:
	go run ./client/ \
		--grpc-target localhost:50051 --token demo-token \
		--envoy-target localhost:50052 \
		--ca-cert deploy/envoy/certs/generated/ca.crt \
		--client-cert deploy/envoy/certs/generated/client.crt \
		--client-key deploy/envoy/certs/generated/client.key
```

Also update the `.PHONY` line: remove `client-envoy`, `unary-grpc`, `unary-envoy`.

- [ ] **Step 3: Verify compilation**

```bash
go build ./client/...
```

Expected: compiles. The old `Model` and `UnaryModel` types are gone, replaced by `UnifiedModel`.

- [ ] **Step 4: Commit**

```bash
git add -A client/tui/ Makefile
git commit -m "feat: remove old models, single make client target"
```

---

### Task 5: Verify End-to-End

- [ ] **Step 1: Build and test**

```bash
go build ./client/...
make client
```

Expected:
- Tab bar shows 4 tabs at the top
- Tab key cycles between modes
- gRPC Unary: send message, get response with pod name
- gRPC Stream: send message, tokens stream, heartbeats appear
- Envoy Unary: send message, pod name rotates (round-robin)
- Envoy Stream: send message, tokens stream, pod name shown
- If Envoy not deployed, Envoy tabs show "Connection failed" instead of crashing
- Ctrl+C cancels generation in stream modes
- Esc quits, closing all streams

- [ ] **Step 2: Commit any fixes**

```bash
git add -A && git commit -m "fix: unified TUI adjustments"
```

---

## Verification Checklist

1. `go build ./client/...` compiles
2. Tab key cycles through 4 modes
3. gRPC Unary works (send message, response with pod)
4. gRPC Stream works (tokens stream, cancel, reconnect)
5. Envoy Unary works (round-robin pod names)
6. Envoy Stream works (mTLS, tokens stream)
7. Background streams keep running when tabbed away
8. Envoy connection failure shows error in those tabs, doesn't crash
9. `make client` is the only client Makefile target
10. Old `model.go` and `unary_model.go` deleted
