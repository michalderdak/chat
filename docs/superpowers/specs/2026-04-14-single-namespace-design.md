# Single Namespace Design Spec

## Overview

Merge the two separate namespaces (`chat-grpc` and `chat-envoy`) into a single `chat` namespace. One set of backend pods serves both direct gRPC and Envoy-proxied traffic. Eliminates duplicated servers, Redis, and gateway.

## Architecture

```
chat namespace:
  Server x3 (plaintext gRPC, auth interceptor always on)
  Redis x1 (conversation history)
  Gateway x1 (HTTP/JSON transcoding)
  Envoy x1 (mTLS termination, round-robin LB)

Two entry points to the SAME pods:
  NodePort 30051 → chat-server (ClusterIP)  → pick-first
  NodePort 30052 → envoy (NodePort)         → chat-server-headless → round-robin
```

## K8s Manifest Changes

### Delete
- `deploy/grpc/` — entire directory
- `deploy/envoy/` — entire directory

### Create: `deploy/chat/`

**`deploy/chat/namespace.yaml`:**
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: chat
```

**`deploy/chat/server-service-nodeport.yaml`** — patch base service to NodePort 30051:
```yaml
apiVersion: v1
kind: Service
metadata:
  name: chat-server
spec:
  type: NodePort
  ports:
    - name: grpc
      port: 50051
      targetPort: 50051
      nodePort: 30051
    - name: metrics
      port: 9090
      targetPort: 9090
```

**`deploy/chat/server-headless-service.yaml`** — headless service for Envoy pod discovery:
```yaml
apiVersion: v1
kind: Service
metadata:
  name: chat-server-headless
spec:
  clusterIP: None
  selector:
    app: chat-server
  ports:
    - name: grpc
      port: 50051
      targetPort: 50051
```

**`deploy/chat/gateway-service-nodeport.yaml`** — NodePort 30080:
```yaml
apiVersion: v1
kind: Service
metadata:
  name: chat-gateway
spec:
  type: NodePort
  ports:
    - name: http
      port: 8080
      targetPort: 8080
      nodePort: 30080
```

**Envoy files** — move from `deploy/envoy/` to `deploy/chat/`:
- `envoy-configmap.yaml` — update `address: chat-server-headless` (dedicated headless service instead of patching the main one)
- `envoy-deployment.yaml` — unchanged
- `envoy-service.yaml` — NodePort 30052, unchanged
- `certs/generate-certs.sh` — update SANs to use `chat` namespace: `DNS:chat-server.chat.svc.cluster.local`

**`deploy/chat/kustomization.yaml`:**
```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: chat
resources:
  - namespace.yaml
  - ../base
  - server-headless-service.yaml
  - envoy-configmap.yaml
  - envoy-deployment.yaml
  - envoy-service.yaml
patches:
  - path: server-service-nodeport.yaml
  - path: gateway-service-nodeport.yaml
```

## What Stays the Same
- `deploy/base/` — server deployment, gateway deployment, Redis, PDB, ollama service. All unchanged.
- Server auth interceptor — always on. Both direct and Envoy traffic require bearer token.
- Envoy config — mTLS termination, round-robin, health checks, per-route timeouts. Envoy points at headless service.
- Go client — no changes. Same 4 tabs, same 2 connections (`--grpc-target :50051`, `--envoy-target :50052`).

## Makefile Changes

- `deploy-grpc` and `deploy-envoy` replaced by single `deploy-chat`
- `deploy-all` calls `deploy-observability` then `deploy-chat`
- `make certs` updates cert SANs for `chat` namespace
- `logs-grpc` and `logs-envoy` replaced by single `make logs`
- `make client` flags unchanged (same ports)

## Kind Config

Port mappings stay the same:
- 30051 → 50051 (direct gRPC)
- 30052 → 50052 (Envoy)
- 30080 → 8080 (gateway)
- 30686 → 16686 (Jaeger)
- 30090 → 9090 (Prometheus)

## Key Design Decisions

1. **One namespace, two services** — ClusterIP for direct access (pick-first), headless for Envoy discovery (round-robin). Same pods serve both.
2. **Auth always on** — simpler than conditional auth. Envoy adds mTLS on top, bearer token still required.
3. **Dedicated headless service** — separate `chat-server-headless` instead of patching the main ClusterIP. Cleaner than the previous approach of patching `clusterIP: None` which can't be changed on existing services.
4. **Envoy cert SANs** — updated to `chat` namespace DNS names.
