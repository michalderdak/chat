package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// UnaryResponseMsg carries the response from a SendMessage RPC
type UnaryResponseMsg struct {
	Text     string
	PodName  string
	Duration time.Duration
}

// SendUnary calls the SendMessage unary RPC and extracts pod name from trailing metadata.
func SendUnary(client chatv1.ChatServiceClient, conversationID, text string) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()

		var trailer metadata.MD
		resp, err := client.SendMessage(
			context.Background(),
			&chatv1.SendMessageRequest{
				ConversationId: conversationID,
				Text:           text,
			},
			grpc.Trailer(&trailer),
		)
		duration := time.Since(start)

		if err != nil {
			return ErrorMsg{Err: fmt.Errorf("SendMessage: %w", err)}
		}

		podName := "unknown"
		if vals := trailer.Get("x-served-by"); len(vals) > 0 {
			podName = vals[0]
		}

		return UnaryResponseMsg{
			Text:     resp.GetText(),
			PodName:  podName,
			Duration: duration,
		}
	}
}
