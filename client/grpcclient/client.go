package grpcclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
)

type Config struct {
	Target     string
	Token      string
	UseTLS     bool
	CACert     string // path to CA certificate
	ClientCert string // path to client certificate
	ClientKey  string // path to client key
	Timeout    time.Duration
}

func NewChatClient(cfg Config) (chatv1.ChatServiceClient, *grpc.ClientConn, error) {
	var opts []grpc.DialOption

	if cfg.UseTLS {
		tlsCfg := &tls.Config{}

		// Load CA cert to trust the server
		if cfg.CACert != "" {
			caCert, err := os.ReadFile(cfg.CACert)
			if err != nil {
				return nil, nil, fmt.Errorf("read CA cert: %w", err)
			}
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caCert) {
				return nil, nil, fmt.Errorf("failed to parse CA cert")
			}
			tlsCfg.RootCAs = pool
		}

		// Load client cert for mTLS
		if cfg.ClientCert != "" && cfg.ClientKey != "" {
			cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
			if err != nil {
				return nil, nil, fmt.Errorf("load client cert: %w", err)
			}
			tlsCfg.Certificates = []tls.Certificate{cert}
		}

		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	if cfg.Token != "" {
		opts = append(opts,
			grpc.WithUnaryInterceptor(authUnaryInterceptor(cfg.Token)),
			grpc.WithStreamInterceptor(authStreamInterceptor(cfg.Token)),
		)
	}

	opts = append(opts,
		grpc.WithChainUnaryInterceptor(loggingUnaryInterceptor()),
		grpc.WithChainStreamInterceptor(loggingStreamInterceptor()),
	)

	conn, err := grpc.NewClient(cfg.Target, opts...)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", cfg.Target, err)
	}

	return chatv1.NewChatServiceClient(conn), conn, nil
}

func authUnaryInterceptor(token string) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func authStreamInterceptor(token string) grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		ctx = metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+token)
		return streamer(ctx, desc, cc, method, opts...)
	}
}

func loggingUnaryInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		start := time.Now()
		err := invoker(ctx, method, req, reply, cc, opts...)
		log.Printf("[gRPC] %s %v (%s)", method, err, time.Since(start))
		return err
	}
}

func loggingStreamInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		log.Printf("[gRPC] stream open: %s", method)
		return streamer(ctx, desc, cc, method, opts...)
	}
}
