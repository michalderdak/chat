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
	chatViewport  viewport.Model
	eventViewport viewport.Model
	input         textinput.Model
	messages      []ChatMessage
	eventLog      []EventEntry

	stream         *grpcclient.StreamClient
	conversationID string
	streaming      bool
	status         string
	err            error

	promptTokens     int
	completionTokens int
	contextLength    int

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
	chrome := statusHeight + inputHeight + 4
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

		if m.streaming {
			if len(m.messages) > 0 {
				last := &m.messages[len(m.messages)-1]
				if last.Role == "assistant" {
					last.Content += " [cancelled]"
				}
			}
			cmds = append(cmds, SendCancel(m.stream, m.conversationID))
		}

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

	chatTitle := PanelTitleStyle.Render(" Chat ")
	chatPanel := PanelBorderStyle.Width(halfWidth - 2).Height(m.chatViewport.Height + 1).Render(
		chatTitle + "\n" + m.chatViewport.View(),
	)

	eventTitle := PanelTitleStyle.Render(" gRPC Event Log ")
	eventPanel := PanelBorderStyle.Width(halfWidth - 2).Height(m.eventViewport.Height + 1).Render(
		eventTitle + "\n" + m.eventViewport.View(),
	)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, chatPanel, eventPanel)

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
