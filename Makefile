.PHONY: generate docker lint breaking cluster build load deploy-grpc deploy-envoy \
        deploy-observability deploy-all client \
        certs clean cluster-clean \
        grpcurl-list grpcurl-health grpcurl-send curl-send \
        logs-grpc logs-envoy logs-envoy-proxy

# Third-party proto includes (googleapis for google.api.http annotations)
# Used by grpc_tools.protoc which cannot resolve buf deps
THIRD_PARTY_DIR ?= /third_party

# --- Code Generation (Docker-based) ---

# This target runs inside the Docker container
generate:
	@rm -rf gen
	@mkdir -p gen/go gen/python gen/openapiv2

	@echo "==> Running buf generate (Go, Python, OpenAPI)..."
	buf generate

	@echo "==> Generating Python gRPC stubs..."
	python3 -m grpc_tools.protoc \
		-I proto \
		-I $(THIRD_PARTY_DIR) \
		--grpc_python_out=gen/python \
		proto/chat/v1/chat.proto

	@echo "==> Creating Python __init__.py files..."
	touch gen/__init__.py gen/python/__init__.py gen/python/chat/__init__.py gen/python/chat/v1/__init__.py

	@echo "==> Generation complete."

# Build Docker image and copy generated files to local directory
docker:
	@rm -rf gen
	docker build --progress plain -t chat-proto-gen .
	@DOCKERID=$$(docker create chat-proto-gen) && \
		docker cp $$DOCKERID:/app/gen ./ && \
		docker rm $$DOCKERID
	@echo "==> Generated files copied to ./gen"

clean:
	rm -rf gen

# --- Buf Lint / Breaking ---

lint:
	buf lint

breaking:
	buf breaking --against '.git#branch=main'

# --- Kind Cluster ---

cluster:
	kind create cluster --name chat-demo --config deploy/kind-config.yaml
	@echo "Cluster created."

cluster-clean:
	kind delete cluster --name chat-demo

# --- Docker Build ---

build:
	docker build -f server/Dockerfile -t chat-server:latest .
	docker build -f gateway/Dockerfile -t chat-gateway:latest .

load: build
	kind load docker-image chat-server:latest --name chat-demo
	kind load docker-image chat-gateway:latest --name chat-demo

# --- Deploy ---

deploy-observability:
	kubectl apply -k deploy/observability/

deploy-grpc: load
	kubectl apply -k deploy/grpc/
	kubectl -n chat-grpc rollout restart deployment/chat-server deployment/chat-gateway
	@echo "Waiting for rollout..."
	kubectl -n chat-grpc rollout status deployment/chat-server --timeout=120s
	kubectl -n chat-grpc rollout status deployment/chat-gateway --timeout=120s

deploy-envoy: load certs
	kubectl create namespace chat-envoy --dry-run=client -o yaml | kubectl apply -f -
	kubectl -n chat-envoy create secret generic envoy-certs \
		--from-file=ca.crt=deploy/envoy/certs/generated/ca.crt \
		--from-file=server.crt=deploy/envoy/certs/generated/server.crt \
		--from-file=server.key=deploy/envoy/certs/generated/server.key \
		--dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -k deploy/envoy/
	kubectl -n chat-envoy rollout restart deployment/chat-server deployment/envoy
	@echo "Waiting for rollout..."
	kubectl -n chat-envoy rollout status deployment/chat-server --timeout=120s
	kubectl -n chat-envoy rollout status deployment/envoy --timeout=120s

deploy-all: deploy-observability deploy-grpc deploy-envoy
	@echo "All deployed."

certs:
	deploy/envoy/certs/generate-certs.sh

# --- Client ---

client:
	go run ./client/ \
		--grpc-target localhost:50051 --token demo-token \
		--envoy-target localhost:50052 \
		--ca-cert deploy/envoy/certs/generated/ca.crt \
		--client-cert deploy/envoy/certs/generated/client.crt \
		--client-key deploy/envoy/certs/generated/client.key

# --- Demo Helpers ---

grpcurl-list:
	grpcurl -plaintext localhost:50051 list

grpcurl-health:
	grpcurl -plaintext localhost:50051 grpc.health.v1.Health/Check

grpcurl-send:
	grpcurl -plaintext -d '{"conversation_id":"test","text":"Hello"}' \
		localhost:50051 chat.v1.ChatService/SendMessage

curl-send:
	curl -X POST http://localhost:8080/v1/chat/send \
		-H 'Content-Type: application/json' \
		-d '{"conversation_id":"test","text":"Hello"}'

# --- Logs ---

logs-grpc:
	kubectl -n chat-grpc logs -l app=chat-server --all-containers -f

logs-envoy:
	kubectl -n chat-envoy logs -l app=chat-server --all-containers -f

logs-envoy-proxy:
	kubectl -n chat-envoy logs -l app=envoy -f

.DEFAULT_GOAL := docker
