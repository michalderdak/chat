FROM golang:1.24-bookworm

ENV GOPATH=/go
ENV PATH=/go/bin:$PATH

RUN mkdir -p ${GOPATH}/src ${GOPATH}/bin

# Install system dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
    python3 \
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

# Install Go protoc plugins (used as local plugins by buf)
RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest && \
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest && \
    go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest

WORKDIR /app

COPY buf.yaml buf.gen.yaml buf.lock ./
COPY proto proto
COPY Makefile ./

RUN make generate
