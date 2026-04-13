package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type Direction int

const (
	Outgoing Direction = iota
	Incoming
)

type EventEntry struct {
	Dir     Direction
	Type    string
	Payload string
}

var (
	OutgoingUserMsgStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	OutgoingCancelStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))
	OutgoingContextStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	IncomingTokenStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("245")).Faint(true)
	IncomingStatusStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))
	IncomingErrorStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	IncomingHeartbeatStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Faint(true)
	IncomingAckStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("14"))
	IncomingUsageStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	IncomingShutdownStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	OutgoingReconnectStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	OutgoingSendMsgStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))
	IncomingResponseStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	ArrowOutStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	ArrowInStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
)

func (e EventEntry) Render() string {
	var arrow string
	if e.Dir == Outgoing {
		arrow = ArrowOutStyle.Render("→")
	} else {
		arrow = ArrowInStyle.Render("←")
	}

	style := e.styleForType()
	var styled string
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
		case "Reconnected", "Connected":
			return OutgoingReconnectStyle
		case "SendMessage":
			return OutgoingSendMsgStyle
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
	case "ServerShutdown":
		return IncomingShutdownStyle
	case "Response":
		return IncomingResponseStyle
	}
	return IncomingTokenStyle
}

func truncate(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", "\\n")
	if len(s) > max {
		return s[:max] + "..."
	}
	return s
}
