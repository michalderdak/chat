package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	chatv1 "github.com/michal-derdak/chat/gen/go/chat/v1"
)

func main() {
	grpcAddr := flag.String("grpc-addr", "localhost:50051", "gRPC server address")
	httpAddr := flag.String("http-addr", ":8080", "HTTP listen address")
	flag.Parse()

	ctx := context.Background()
	mux := runtime.NewServeMux(
		runtime.WithIncomingHeaderMatcher(func(key string) (string, bool) {
			if key == "Authorization" {
				return key, true
			}
			return runtime.DefaultHeaderMatcher(key)
		}),
	)

	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

	err := chatv1.RegisterChatServiceHandlerFromEndpoint(ctx, mux, *grpcAddr, opts)
	if err != nil {
		log.Fatalf("Failed to register gateway: %v", err)
	}

	log.Printf("Gateway listening on %s, proxying gRPC at %s", *httpAddr, *grpcAddr)
	if err := http.ListenAndServe(*httpAddr, mux); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}
