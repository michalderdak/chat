package main

import (
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
	grpcTarget := flag.String("grpc-target", "localhost:50051", "gRPC server address (plaintext)")
	envoyTarget := flag.String("envoy-target", "localhost:50052", "Envoy proxy address (mTLS)")
	token := flag.String("token", "demo-token", "Bearer token for auth")
	caCert := flag.String("ca-cert", "", "Path to CA certificate (for Envoy mTLS)")
	clientCert := flag.String("client-cert", "", "Path to client certificate (for Envoy mTLS)")
	clientKey := flag.String("client-key", "", "Path to client key (for Envoy mTLS)")
	timeout := flag.Duration("timeout", 30*time.Minute, "Stream timeout")
	flag.Parse()

	logFile, err := os.OpenFile("client.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()
	log.SetOutput(logFile)

	// Connection 1: plaintext gRPC
	grpcClient, grpcConn, grpcErr := grpcclient.NewChatClient(grpcclient.Config{
		Target:  *grpcTarget,
		Token:   *token,
		UseTLS:  false,
		Timeout: *timeout,
	})
	if grpcConn != nil {
		defer grpcConn.Close()
	}

	// Connection 2: mTLS Envoy
	envoyClient, envoyConn, envoyErr := grpcclient.NewChatClient(grpcclient.Config{
		Target:     *envoyTarget,
		Token:      *token,
		UseTLS:     true,
		CACert:     *caCert,
		ClientCert: *clientCert,
		ClientKey:  *clientKey,
		Timeout:    *timeout,
	})
	if envoyConn != nil {
		defer envoyConn.Close()
	}

	model := tui.NewUnifiedModel(grpcClient, grpcErr, envoyClient, envoyErr, *timeout)
	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
