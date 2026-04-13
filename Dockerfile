FROM golang:1.24-bookworm

ENV GOPATH=/go
ENV PATH=/go/bin:$PATH

RUN mkdir -p ${GOPATH}/src ${GOPATH}/bin

# Install system dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    python3 \
    python3-pip \
    python3-venv \
    make \
    unzip \
    curl && \
    rm -rf /var/lib/apt/lists/*

# Install buf
ARG BUF_VERSION=1.47.2
RUN curl -sSL \
    "https://github.com/bufbuild/buf/releases/download/v${BUF_VERSION}/buf-$(uname -s)-$(uname -m)" \
    -o /usr/local/bin/buf && \
    chmod +x /usr/local/bin/buf

# Install Go protoc plugins
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest

# Install protoc (needed for buf protoc_builtin plugins)
ARG PROTOC_VERSION=28.3
RUN ARCH=$(uname -m) && \
    if [ "$ARCH" = "aarch64" ]; then PROTOC_ARCH="aarch_64"; elif [ "$ARCH" = "x86_64" ]; then PROTOC_ARCH="x86_64"; fi && \
    curl -sSL "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-${PROTOC_ARCH}.zip" \
    -o /tmp/protoc.zip && \
    unzip /tmp/protoc.zip -d /usr/local && \
    rm /tmp/protoc.zip

# Install Python grpcio-tools for gRPC Python stub generation
RUN pip3 install --break-system-packages grpcio-tools

# Download googleapis proto files needed for grpc_tools.protoc
# (buf handles these as deps for buf generate, but grpc_tools.protoc needs them on the filesystem)
RUN mkdir -p /third_party/google/api && \
    curl -sSL "https://raw.githubusercontent.com/googleapis/googleapis/master/google/api/annotations.proto" \
    -o /third_party/google/api/annotations.proto && \
    curl -sSL "https://raw.githubusercontent.com/googleapis/googleapis/master/google/api/http.proto" \
    -o /third_party/google/api/http.proto

WORKDIR /app

COPY buf.yaml buf.gen.yaml buf.lock ./
COPY proto proto
COPY Makefile ./

RUN make generate
