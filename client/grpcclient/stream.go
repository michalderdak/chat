package grpcclient

import (
	"context"
	"fmt"
	"io"
	"time"

	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
)

type StreamClient struct {
	stream chatv1.ChatService_ChatClient
	cancel context.CancelFunc
}

func OpenStream(client chatv1.ChatServiceClient, timeout time.Duration) (*StreamClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	stream, err := client.Chat(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("open stream: %w", err)
	}

	return &StreamClient{stream: stream, cancel: cancel}, nil
}

func (s *StreamClient) Send(msg *chatv1.ChatRequest) error {
	return s.stream.Send(msg)
}

func (s *StreamClient) Recv() (*chatv1.ChatResponse, error) {
	return s.stream.Recv()
}

func (s *StreamClient) Close() {
	s.stream.CloseSend()
	s.cancel()
}

func (s *StreamClient) IsEOF(err error) bool {
	return err == io.EOF
}

// PodName returns the x-served-by value from initial metadata (response headers).
func (s *StreamClient) PodName() string {
	md, err := s.stream.Header()
	if err != nil {
		return "unknown"
	}
	if vals := md.Get("x-served-by"); len(vals) > 0 {
		return vals[0]
	}
	return "unknown"
}
