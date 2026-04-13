package main

import (
	"crypto/rand"
	"encoding/hex"
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
	target := flag.String("target", "localhost:50051", "gRPC server address")
	token := flag.String("token", "demo-token", "Bearer token for auth")
	useTLS := flag.Bool("tls", false, "Use TLS")
	caCert := flag.String("ca-cert", "", "Path to CA certificate (for TLS)")
	clientCert := flag.String("client-cert", "", "Path to client certificate (for mTLS)")
	clientKey := flag.String("client-key", "", "Path to client key (for mTLS)")
	timeout := flag.Duration("timeout", 30*time.Minute, "Stream timeout")
	flag.Parse()

	logFile, err := os.OpenFile("client.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	client, conn, err := grpcclient.NewChatClient(grpcclient.Config{
		Target:     *target,
		Token:      *token,
		UseTLS:     *useTLS,
		CACert:     *caCert,
		ClientCert: *clientCert,
		ClientKey:  *clientKey,
		Timeout:    *timeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	stream, err := grpcclient.OpenStream(client, *timeout)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open stream: %v\n", err)
		os.Exit(1)
	}

	idBytes := make([]byte, 8)
	rand.Read(idBytes)
	conversationID := "chat-" + hex.EncodeToString(idBytes)

	model := tui.NewModel(client, stream, conversationID, *timeout)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
