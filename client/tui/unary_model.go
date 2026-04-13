package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
)

type UnaryModel struct {
	chatViewport  viewport.Model
	eventViewport viewport.Model
	input         textinput.Model
	messages      []ChatMessage
	eventLog      []EventEntry

	grpcClient     chatv1.ChatServiceClient
	conversationID string
	waiting        bool
	status         string
	err            error

	ready  bool
	width  int
	height int
}

func NewUnaryModel(grpcClient chatv1.ChatServiceClient, conversationID string) UnaryModel {
	ti := textinput.New()
	ti.Placeholder = "Type a message... (Enter to send, Esc to quit)"
	ti.Focus()
	ti.Width = 80

	return UnaryModel{
		input:          ti,
		messages:       []ChatMessage{},
		eventLog:       []EventEntry{},
		status:         "ready (unary mode)",
		grpcClient:     grpcClient,
		conversationID: conversationID,
	}
}

func (m UnaryModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m UnaryModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m.handleResize(), nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case UnaryResponseMsg:
		m.addEvent(EventEntry{
			Dir:     Incoming,
			Type:    "Response",
			Payload: fmt.Sprintf("(%s) %.1fs", msg.PodName, msg.Duration.Seconds()),
		})
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.Role == "assistant" {
				last.Content = msg.Text
			}
		}
		m.waiting = false
		m.status = fmt.Sprintf("ready | last pod: %s", msg.PodName)
		m.refreshPanels()
		return m, nil

	case ErrorMsg:
		m.addEvent(EventEntry{Dir: Incoming, Type: "Error", Payload: msg.Err.Error()})
		m.err = msg.Err
		m.waiting = false
		m.status = fmt.Sprintf("error: %s", msg.Err)
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

func (m *UnaryModel) handleResize() UnaryModel {
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

func (m *UnaryModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "esc":
		return m, tea.Quit
	case "enter":
		text := strings.TrimSpace(m.input.Value())
		if text == "" || m.waiting {
			return m, nil
		}
		m.input.Reset()

		m.addEvent(EventEntry{
			Dir:     Outgoing,
			Type:    "SendMessage",
			Payload: fmt.Sprintf("%q", truncate(text, 30)),
		})
		m.messages = append(m.messages, ChatMessage{Role: "user", Content: text})
		m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: "..."})
		m.waiting = true
		m.status = "waiting for response..."
		m.refreshPanels()

		return m, SendUnary(m.grpcClient, m.conversationID, text)
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

func (m *UnaryModel) addEvent(e EventEntry) {
	m.eventLog = append(m.eventLog, e)
}

func (m *UnaryModel) refreshPanels() {
	m.chatViewport.SetContent(m.renderMessages())
	m.chatViewport.GotoBottom()
	m.eventViewport.SetContent(m.renderEventLog())
	m.eventViewport.GotoBottom()
}

func (m UnaryModel) View() string {
	if !m.ready {
		return "Initializing..."
	}

	halfWidth := m.width / 2

	chatTitle := PanelTitleStyle.Render(" Chat (Unary) ")
	chatPanel := PanelBorderStyle.Width(halfWidth - 2).Height(m.chatViewport.Height + 1).Render(
		chatTitle + "\n" + m.chatViewport.View(),
	)

	eventTitle := PanelTitleStyle.Render(" gRPC Event Log ")
	eventPanel := PanelBorderStyle.Width(halfWidth - 2).Height(m.eventViewport.Height + 1).Render(
		eventTitle + "\n" + m.eventViewport.View(),
	)

	panels := lipgloss.JoinHorizontal(lipgloss.Top, chatPanel, eventPanel)

	statusBar := StatusBarStyle.Width(m.width).Render(
		fmt.Sprintf(" %s | waiting: %v", m.status, m.waiting),
	)

	return fmt.Sprintf("%s\n%s\n%s", panels, statusBar, m.input.View())
}

func (m UnaryModel) renderMessages() string {
	wrapWidth := m.chatViewport.Width - 4
	if wrapWidth < 10 {
		wrapWidth = 40
	}
	var b strings.Builder
	for _, msg := range m.messages {
		var prefix, content string
		switch msg.Role {
		case "user":
			prefix = UserStyle.Render("You: ")
			content = msg.Content
		case "assistant":
			prefix = AssistantStyle.Render("AI: ")
			content = msg.Content
		}
		b.WriteString(prefix)
		b.WriteString(wordWrap(content, wrapWidth-6))
		b.WriteString("\n\n")
	}
	return b.String()
}

func (m UnaryModel) renderEventLog() string {
	wrapWidth := m.eventViewport.Width - 4
	if wrapWidth < 10 {
		wrapWidth = 40
	}
	var b strings.Builder
	for _, e := range m.eventLog {
		b.WriteString(wordWrap(e.Render(), wrapWidth))
		b.WriteString("\n")
	}
	return b.String()
}
