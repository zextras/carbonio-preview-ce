# gRPC Consul Service Configuration Notes

This document records the single-port coexistence architecture for
carbonio-preview-ce: gRPC + REST + health are all multiplexed on a SINGLE port
(default 10000) via `github.com/soheilhy/cmux`. This matches the Carbonio
precedent set by carbonio-user-management and carbonio-videorecorder
(`use-separate-server=false`).

## Single port

All traffic (gRPC, REST, health) shares `cfg.ServiceIP:cfg.ServicePort`
(default `127.78.0.6:10000`). The separate `grpc-port` config key (formerly
default 10001) has been removed. The cmux multiplexer routes:
- `content-type: application/grpc` + HTTP/2 settings → native `grpc.Server`
- everything else → `http.Server` (REST mux + health endpoints)

The `GRPCPort` config field is gone. Consumer mesh upstreams use the standard
single `{destination_name=carbonio-preview, local_bind_port}` block forwarding
to port 10000.

## Service mesh protocol progression

`service-protocol.json` follows a three-phase progression:

### Now (coexistence phase — REST + gRPC)
```json
{
  "kind": "service-defaults",
  "name": "carbonio-preview",
  "protocol": "http"
}
```
Keep `http` while REST consumers are live. REST routes through the Envoy mesh
normally (HTTP/1.1). gRPC can be tested directly or via a separate upstream.
Both share port 10000.

### Phase 8 — consumer migration (REST still alive, gRPC primary)
Switch to `"protocol": "tcp"` so the sidecar proxy tunnels raw bytes (both
HTTP/2 gRPC and HTTP/1.1 REST pass through). Consul intentions become
service-level (the `{files,wsc,mailbox}` allow-list continues to work).
HTTP health check stays on the same port (`/health/live/`), hit directly
(not through the mesh proxy).

### Phase 9 — after REST removal (gRPC only)
Switch to `"protocol": "grpc"` once REST endpoints are gone, matching
carbonio-user-management and carbonio-videorecorder. Update `carbonio-preview.hcl`
health check to the gRPC probe:
```hcl
check {
  grpc         = "127.78.0.6:10000"
  grpc_use_tls = false
  timeout      = "1s"
  interval     = "5s"
}
```

## Health checks (current — coexistence)

Single HTTP health check remains unchanged:
```hcl
check {
  http     = "http://127.78.0.6:10000/health/live/"
  method   = "GET"
  timeout  = "1s"
  interval = "5s"
}
```

The gRPC health service (`grpc.health.v1.Health/Check`) is also available on
the same port 10000 for direct probing (e.g. grpcurl).

## No more separate gRPC port

The `carbonio-preview/grpc-port` Consul KV key and `APPLICATION_CONFIG_GRPC_PORT`
env var are **removed**. If they exist in KV from a prior deployment, they are
inert and can be deleted.
