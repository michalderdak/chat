#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)/generated"
rm -rf "$DIR"
mkdir -p "$DIR"

echo "Generating certs in $DIR"

# CA key and self-signed cert (two-step for Go compatibility)
openssl genrsa -out "$DIR/ca.key" 4096 2>/dev/null
openssl req -new -x509 -sha256 -key "$DIR/ca.key" -out "$DIR/ca.crt" \
  -days 365 -subj "/CN=chat-demo-ca" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null

# Server key and cert
openssl genrsa -out "$DIR/server.key" 2048 2>/dev/null
openssl req -new -sha256 -key "$DIR/server.key" -out "$DIR/server.csr" \
  -subj "/CN=chat-server" 2>/dev/null
openssl x509 -req -sha256 -in "$DIR/server.csr" -CA "$DIR/ca.crt" -CAkey "$DIR/ca.key" \
  -CAcreateserial -out "$DIR/server.crt" -days 365 \
  -extfile <(printf "subjectAltName=DNS:chat-server,DNS:chat-server.chat.svc.cluster.local,DNS:chat-server-headless,DNS:chat-server-headless.chat.svc.cluster.local,DNS:envoy,DNS:envoy.chat.svc.cluster.local,DNS:localhost") 2>/dev/null

# Client key and cert
openssl genrsa -out "$DIR/client.key" 2048 2>/dev/null
openssl req -new -sha256 -key "$DIR/client.key" -out "$DIR/client.csr" \
  -subj "/CN=chat-client" 2>/dev/null
openssl x509 -req -sha256 -in "$DIR/client.csr" -CA "$DIR/ca.crt" -CAkey "$DIR/ca.key" \
  -CAcreateserial -out "$DIR/client.crt" -days 365 2>/dev/null

rm -f "$DIR"/*.csr "$DIR"/*.srl
echo "Done: ca.crt, server.crt, server.key, client.crt, client.key"
