package tui

import (
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"
	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
	"github.com/michal-derdak/chat/client/grpcclient"
)

type TokenMsg struct{ Text string }
type StatusMsg struct{ Phase string }
type ErrorMsg struct{ Err error }
type StreamEndMsg struct{}
type AckMsg struct{ Type string }
type HeartbeatMsg struct{ Beat string }
type UsageMsg struct {
	PromptTokens     int
	CompletionTokens int
	ContextLength    int
}
type EventLogMsg struct{ Entry EventEntry }

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
				PromptTokens:     int(evt.Usage.GetPromptTokens()),
				CompletionTokens: int(evt.Usage.GetCompletionTokens()),
				ContextLength:    int(evt.Usage.GetContextLength()),
			}
		default:
			return nil
		}
	}
}

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
