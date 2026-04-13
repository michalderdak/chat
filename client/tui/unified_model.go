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
	ConnErr        error

	Messages       []ChatMessage
	EventLog       []EventEntry
	Stream         *grpcclient.StreamClient
	ConversationID string
	PodName        string
	Streaming      bool
	Waiting        bool
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
		for _, ms := range m.modes {
			if ms.Stream != nil {
				ms.Stream.Close()
			}
		}
		return m, tea.Quit

	case "enter":
		if mode.ConnErr != nil {
			return m, nil
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

	tabBar := m.renderTabBar()

	chatTitle := PanelTitleStyle.Render(fmt.Sprintf(" Chat (%s) ", mode.Name))
	chatPanel := PanelBorderStyle.Width(halfWidth - 2).Height(m.chatViewport.Height + 1).Render(
		chatTitle + "\n" + m.chatViewport.View(),
	)

	eventTitle := PanelTitleStyle.Render(" gRPC Event Log ")
	eventPanel := PanelBorderStyle.Width(halfWidth - 2).Height(m.eventViewport.Height + 1).Render(
		eventTitle + "\n" + m.eventViewport.View(),
	)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, chatPanel, eventPanel)

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
