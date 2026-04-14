# Single Namespace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Merge `chat-grpc` and `chat-envoy` into a single `chat` namespace with shared backend pods, one Redis, and Envoy as a front proxy alongside direct gRPC access.

**Architecture:** Delete the two namespace overlays. Create a single `deploy/chat/` overlay that includes base resources + Envoy + headless service + NodePort patches. Update Makefile, Prometheus config, and cert SANs. Go client unchanged.

**Tech Stack:** Kustomize, Envoy, Kind

---

## File Structure

```
Delete:
  deploy/grpc/                          — entire directory
  deploy/envoy/                         — entire directory (contents move to deploy/chat/)

Create:
  deploy/chat/namespace.yaml
  deploy/chat/server-service-nodeport.yaml
  deploy/chat/server-headless-service.yaml
  deploy/chat/gateway-service-nodeport.yaml
  deploy/chat/envoy-configmap.yaml
  deploy/chat/envoy-deployment.yaml
  deploy/chat/envoy-service.yaml
  deploy/chat/certs/generate-certs.sh
  deploy/chat/kustomization.yaml

Modify:
  deploy/observability/prometheus/configmap.yaml  — namespace references
  Makefile                                         — deploy targets, log targets
```

---

### Task 1: Create deploy/chat/ Overlay

**Files:**
- Create: `deploy/chat/namespace.yaml`
- Create: `deploy/chat/server-service-nodeport.yaml`
- Create: `deploy/chat/server-headless-service.yaml`
- Create: `deploy/chat/gateway-service-nodeport.yaml`
- Create: `deploy/chat/envoy-configmap.yaml`
- Create: `deploy/chat/envoy-deployment.yaml`
- Create: `deploy/chat/envoy-service.yaml`
- Create: `deploy/chat/certs/generate-certs.sh`
- Create: `deploy/chat/kustomization.yaml`

- [ ] **Step 1: Create deploy/chat/namespace.yaml**

```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: chat
```

- [ ] **Step 2: Create deploy/chat/server-service-nodeport.yaml**

This patches the base `chat-server` ClusterIP service to NodePort 30051 for direct gRPC access:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: chat-server
spec:
  type: NodePort
  selector:
    app: chat-server
  ports:
    - name: grpc
      port: 50051
      targetPort: 50051
      nodePort: 30051
    - name: metrics
      port: 9090
      targetPort: 9090
```

- [ ] **Step 3: Create deploy/chat/server-headless-service.yaml**

New headless service for Envoy pod discovery (separate from the main ClusterIP):

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

- [ ] **Step 4: Create deploy/chat/gateway-service-nodeport.yaml**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: chat-gateway
spec:
  type: NodePort
  selector:
    app: chat-gateway
  ports:
    - name: http
      port: 8080
      targetPort: 8080
      nodePort: 30080
```

- [ ] **Step 5: Create deploy/chat/envoy-configmap.yaml**

Copy from `deploy/envoy/envoy-configmap.yaml` but change the backend address to `chat-server-headless`:

Read the current `deploy/envoy/envoy-configmap.yaml`. Copy it to `deploy/chat/envoy-configmap.yaml`. Change the line:

```yaml
                        socket_address: { address: chat-server, port_value: 50051 }
```

to:

```yaml
                        socket_address: { address: chat-server-headless, port_value: 50051 }
```

Everything else stays the same (mTLS, route timeouts, keepalive, preconnect).

- [ ] **Step 6: Create deploy/chat/envoy-deployment.yaml**

Copy `deploy/envoy/envoy-deployment.yaml` as-is to `deploy/chat/envoy-deployment.yaml`. No changes needed.

- [ ] **Step 7: Create deploy/chat/envoy-service.yaml**

Copy `deploy/envoy/envoy-service.yaml` as-is to `deploy/chat/envoy-service.yaml`. No changes needed (NodePort 30052).

- [ ] **Step 8: Create deploy/chat/certs/generate-certs.sh**

Copy `deploy/envoy/certs/generate-certs.sh` to `deploy/chat/certs/generate-certs.sh`. Update the server cert SANs to use the `chat` namespace:

Change:
```
DNS:chat-server,DNS:chat-server.chat-envoy.svc.cluster.local,DNS:envoy,DNS:localhost
```

To:
```
DNS:chat-server,DNS:chat-server.chat.svc.cluster.local,DNS:chat-server-headless,DNS:chat-server-headless.chat.svc.cluster.local,DNS:envoy,DNS:envoy.chat.svc.cluster.local,DNS:localhost
```

Make executable: `chmod +x deploy/chat/certs/generate-certs.sh`

- [ ] **Step 9: Create deploy/chat/kustomization.yaml**

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

- [ ] **Step 10: Validate kustomize**

```bash
kubectl kustomize deploy/chat/
```

Expected: renders all resources under `chat` namespace — server x3, gateway, Redis, PDB, ollama, Envoy, headless service.

- [ ] **Step 11: Commit**

```bash
git add deploy/chat/
git commit -m "feat: create unified chat namespace overlay"
```

---

### Task 2: Delete Old Namespace Overlays

**Files:**
- Delete: `deploy/grpc/` (entire directory)
- Delete: `deploy/envoy/` (entire directory)

- [ ] **Step 1: Delete old directories**

```bash
rm -rf deploy/grpc deploy/envoy
```

- [ ] **Step 2: Commit**

```bash
git add -A deploy/grpc deploy/envoy
git commit -m "feat: remove old chat-grpc and chat-envoy namespace overlays"
```

---

### Task 3: Update Prometheus Config

**Files:**
- Modify: `deploy/observability/prometheus/configmap.yaml`

- [ ] **Step 1: Update namespace references**

Read `deploy/observability/prometheus/configmap.yaml`. Replace all occurrences of `chat-grpc` and `chat-envoy` with `chat`.

The three scrape jobs should all reference `names: ['chat']`:

- `chat-server-grpc` job → `names: ['chat']`
- `chat-server-envoy` job → `names: ['chat']`
- `envoy-admin` job → `names: ['chat']`

Also consider merging the first two jobs into one since they're now the same namespace. Replace the two `chat-server-*` jobs with a single job:

```yaml
      - job_name: 'chat-server'
        kubernetes_sd_configs:
          - role: pod
            namespaces:
              names: ['chat']
        relabel_configs:
          - source_labels: [__meta_kubernetes_pod_annotation_prometheus_io_scrape]
            action: keep
            regex: true
          - source_labels: [__meta_kubernetes_pod_ip, __meta_kubernetes_pod_annotation_prometheus_io_port]
            action: replace
            regex: (.+);(.+)
            target_label: __address__
            replacement: $1:$2
          - source_labels: [__meta_kubernetes_namespace]
            target_label: namespace
          - source_labels: [__meta_kubernetes_pod_name]
            target_label: pod
```

And update the envoy-admin job:
```yaml
      - job_name: 'envoy-admin'
        kubernetes_sd_configs:
          - role: pod
            namespaces:
              names: ['chat']
```

- [ ] **Step 2: Validate**

```bash
kubectl kustomize deploy/observability/
```

- [ ] **Step 3: Commit**

```bash
git add deploy/observability/prometheus/configmap.yaml
git commit -m "feat: update Prometheus config for unified chat namespace"
```

---

### Task 4: Update Makefile

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Update deploy targets and log targets**

Read the Makefile. Make these changes:

**`.PHONY` line**: Replace `deploy-grpc deploy-envoy` with `deploy-chat`. Replace `logs-grpc logs-envoy logs-envoy-proxy` with `logs logs-envoy-proxy`.

**Replace `deploy-grpc` and `deploy-envoy` targets** with single `deploy-chat`:

```makefile
deploy-chat: load certs
	kubectl create namespace chat --dry-run=client -o yaml | kubectl apply -f -
	kubectl -n chat create secret generic envoy-certs \
		--from-file=ca.crt=deploy/chat/certs/generated/ca.crt \
		--from-file=server.crt=deploy/chat/certs/generated/server.crt \
		--from-file=server.key=deploy/chat/certs/generated/server.key \
		--dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -k deploy/chat/
	kubectl -n chat rollout restart deployment/chat-server deployment/chat-gateway deployment/envoy
	@echo "Waiting for rollout..."
	kubectl -n chat rollout status deployment/chat-server --timeout=120s
	kubectl -n chat rollout status deployment/chat-gateway --timeout=120s
	kubectl -n chat rollout status deployment/envoy --timeout=120s
```

**Update `deploy-all`:**

```makefile
deploy-all: deploy-observability deploy-chat
	@echo "All deployed."
```

**Update `certs`:**

```makefile
certs:
	deploy/chat/certs/generate-certs.sh
```

**Replace log targets:**

```makefile
logs:
	kubectl -n chat logs -l app=chat-server --all-containers -f

logs-envoy-proxy:
	kubectl -n chat logs -l app=envoy -f
```

**Update grpcurl/curl targets** if they reference namespace (check and update any `-n chat-grpc` or `-n chat-envoy` to `-n chat`).

- [ ] **Step 2: Verify make targets parse**

```bash
make -n deploy-chat
make -n deploy-all
```

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "feat: update Makefile for unified chat namespace"
```

---

### Task 5: Clean Up Old Namespaces & Deploy

- [ ] **Step 1: Delete old namespaces from the cluster**

```bash
kubectl delete namespace chat-grpc --ignore-not-found
kubectl delete namespace chat-envoy --ignore-not-found
```

- [ ] **Step 2: Deploy everything**

```bash
make deploy-all
```

- [ ] **Step 3: Verify all pods running**

```bash
kubectl -n chat get pods
```

Expected: 3 chat-server pods, 1 gateway, 1 envoy, 1 redis — all Running.

- [ ] **Step 4: Verify both access paths work**

```bash
# Direct gRPC (port 50051)
grpcurl -plaintext -H 'authorization: Bearer demo-token' \
  -import-path proto \
  -import-path $(find ~/.cache/buf -path "*/googleapis/*/files" | head -1) \
  -proto chat/v1/chat.proto \
  -d '{"conversation_id":"test","text":"hey"}' \
  localhost:50051 chat.v1.ChatService/SendMessage

# Via Envoy (port 50052, mTLS)
grpcurl \
  -cacert deploy/chat/certs/generated/ca.crt \
  -cert deploy/chat/certs/generated/client.crt \
  -key deploy/chat/certs/generated/client.key \
  -import-path proto \
  -import-path $(find ~/.cache/buf -path "*/googleapis/*/files" | head -1) \
  -proto chat/v1/chat.proto \
  -d '{"conversation_id":"test","text":"hey"}' \
  localhost:50052 chat.v1.ChatService/SendMessage
```

Both should return a response from the same set of pods.

- [ ] **Step 5: Test unified TUI**

```bash
make client
```

All 4 tabs should work — gRPC Unary/Stream via port 50051, Envoy Unary/Stream via port 50052. Same pods serve both.

- [ ] **Step 6: Commit any fixes**

```bash
git add -A && git commit -m "fix: single namespace deployment adjustments"
```

---

## Verification Checklist

1. `kubectl kustomize deploy/chat/` renders cleanly
2. `deploy/grpc/` and `deploy/envoy/` directories deleted
3. Old `chat-grpc` and `chat-envoy` namespaces deleted from cluster
4. Single `chat` namespace has: server x3, gateway, envoy, redis, PDB
5. Direct gRPC (port 50051) works
6. Envoy mTLS (port 50052) works
7. `make client` — all 4 tabs work
8. `make logs` shows server logs
9. Prometheus scrapes from `chat` namespace
10. `make deploy-all` is a single command that deploys everything
