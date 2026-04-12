#!/usr/bin/env bash
set -euo pipefail

DIR="$(cd "$(dirname "$0")" && pwd)/generated"
mkdir -p "$DIR"

echo "Generating certs in $DIR"

# CA
openssl req -x509 -newkey rsa:2048 -keyout "$DIR/ca.key" -out "$DIR/ca.crt" \
  -days 365 -nodes -subj "/CN=chat-demo-ca" 2>/dev/null

# Server cert
openssl req -newkey rsa:2048 -keyout "$DIR/server.key" -out "$DIR/server.csr" \
  -nodes -subj "/CN=chat-server" 2>/dev/null
openssl x509 -req -in "$DIR/server.csr" -CA "$DIR/ca.crt" -CAkey "$DIR/ca.key" \
  -CAcreateserial -out "$DIR/server.crt" -days 365 \
  -extfile <(printf "subjectAltName=DNS:chat-server,DNS:chat-server.chat-envoy.svc.cluster.local,DNS:envoy,DNS:localhost") 2>/dev/null

# Client cert
openssl req -newkey rsa:2048 -keyout "$DIR/client.key" -out "$DIR/client.csr" \
  -nodes -subj "/CN=chat-client" 2>/dev/null
openssl x509 -req -in "$DIR/client.csr" -CA "$DIR/ca.crt" -CAkey "$DIR/ca.key" \
  -CAcreateserial -out "$DIR/client.crt" -days 365 2>/dev/null

rm -f "$DIR"/*.csr "$DIR"/*.srl
echo "Done: ca.crt, server.crt, server.key, client.crt, client.key"
