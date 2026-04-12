.PHONY: generate docker clean

# Third-party proto includes (googleapis for google.api.http annotations)
# Used by grpc_tools.protoc which cannot resolve buf deps
THIRD_PARTY_DIR ?= /third_party

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

.DEFAULT_GOAL := docker
