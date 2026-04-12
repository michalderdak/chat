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
			return StatusMsg{Phase: "heartbeat"}
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
		return nil
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
		return nil
	}
}
