package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/michal-derdak/chat/client/grpcclient"
)

type ChatMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

type Model struct {
	input          textinput.Model
	viewport       viewport.Model
	messages       []ChatMessage
	streaming      bool
	status         string
	err            error
	stream         *grpcclient.StreamClient
	conversationID string
	ready          bool
	width          int
	height         int
}

func NewModel(stream *grpcclient.StreamClient, conversationID string) Model {
	ti := textinput.New()
	ti.Placeholder = "Type a message... (Enter to send, Ctrl+C to cancel, Esc to quit)"
	ti.Focus()
	ti.Width = 80

	return Model{
		input:          ti,
		messages:       []ChatMessage{},
		status:         "connected",
		stream:         stream,
		conversationID: conversationID,
	}
}

func (m Model) Init() tea.Cmd {
	return textinput.Blink
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerHeight := 1
		inputHeight := 1
		verticalMargin := headerHeight + inputHeight + 2

		if !m.ready {
			m.viewport = viewport.New(msg.Width, msg.Height-verticalMargin)
			m.viewport.SetContent(m.renderMessages())
			m.ready = true
		} else {
			m.viewport.Width = msg.Width
			m.viewport.Height = msg.Height - verticalMargin
		}
		return m, nil

	case tea.KeyMsg:
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
			if text == "" || m.streaming {
				return m, nil
			}
			m.input.Reset()
			m.messages = append(m.messages, ChatMessage{Role: "user", Content: text})
			m.messages = append(m.messages, ChatMessage{Role: "assistant", Content: ""})
			m.streaming = true
			m.status = "sending..."
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()

			return m, tea.Batch(
				SendMessage(m.stream, m.conversationID, text),
				WaitForEvent(m.stream),
			)
		}

	case TokenMsg:
		if len(m.messages) > 0 {
			last := &m.messages[len(m.messages)-1]
			if last.Role == "assistant" {
				last.Content += msg.Text
			}
		}
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, WaitForEvent(m.stream)

	case StatusMsg:
		m.status = msg.Phase
		if msg.Phase == "PHASE_DONE" {
			m.streaming = false
			m.status = "ready"
		}
		return m, WaitForEvent(m.stream)

	case AckMsg:
		m.status = fmt.Sprintf("ack: %s", msg.Type)
		if msg.Type == "cancel" {
			m.streaming = false
			m.status = "cancelled"
		}
		return m, WaitForEvent(m.stream)

	case ErrorMsg:
		m.err = msg.Err
		m.streaming = false
		m.status = fmt.Sprintf("error: %s", msg.Err)
		return m, nil

	case StreamEndMsg:
		m.streaming = false
		m.status = "stream ended"
		return m, nil
	}

	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m Model) View() string {
	if !m.ready {
		return "Initializing..."
	}

	statusBar := StatusBarStyle.Width(m.width).Render(
		fmt.Sprintf(" %s | streaming: %v", m.status, m.streaming),
	)

	return fmt.Sprintf("%s\n%s\n%s",
		statusBar,
		m.viewport.View(),
		m.input.View(),
	)
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
