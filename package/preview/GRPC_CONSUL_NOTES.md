# gRPC Consul Service Configuration Notes

This document records the decisions made when adding the gRPC server alongside
the existing REST server. It is a coexistence-phase document: the REST server
stays alive and the HTTP health check stays intact until the REST→gRPC
migration is complete in a later phase.

## Default gRPC port

The gRPC server listens on port **10001** by default (REST is on 10000).
This is the default encoded in the registry (`config/registry.go`,
key `grpc-port`, `NamespaceApplication`). The final deployed port is
confirmed at package/deploy time and overridden via:

- Consul KV: `carbonio-preview/grpc-port`
- Environment: `APPLICATION_CONFIG_GRPC_PORT`

No change to `carbonio-preview.hcl` is needed for the coexistence phase —
the existing `port = 10000` line refers to the REST service port used by
Consul Connect for mesh routing.

## Health checks (coexistence phase)

During coexistence both health checks run in PARALLEL:

1. **HTTP health check** (existing, must NOT be removed):
   ```hcl
   check {
     http     = "http://127.78.0.6:10000/health/live/"
     method   = "GET"
     timeout  = "1s"
     interval = "5s"
   }
   ```

2. **gRPC health check** (NEW — add to carbonio-preview.hcl when deploying gRPC):
   ```hcl
   check {
     grpc              = "127.78.0.6:10001"
     grpc_use_tls      = false
     timeout           = "1s"
     interval          = "5s"
   }
   ```
   This probes `grpc.health.v1.Health/Check` (overall service status).
   The gRPC server sets status to SERVING immediately after render init.

   **Do NOT replace** the HTTP check with the gRPC check in this phase.
   Add it alongside.

## Service mesh protocol (service-protocol.json)

`service-protocol.json` MUST remain `"protocol": "http"` during coexistence:

```json
{
  "kind": "service-defaults",
  "name": "carbonio-preview",
  "protocol": "http"
}
```

The protocol flips to `"grpc"` ONLY when the REST server is removed in a
later migration phase. Until then the sidecar proxy routes HTTP traffic
and gRPC traffic bypasses the mesh (direct port 10001 or via a separate
service registration).

## Intentions

`intentions.json` is unchanged — intentions apply at the service level, not
per-protocol. No update needed during coexistence.

## Future: when REST is removed

1. Flip `service-protocol.json` → `"protocol": "grpc"`
2. Update `carbonio-preview.hcl` `port` to the gRPC port
3. Remove the HTTP health check, keep only the gRPC one
4. Update the sidecar proxy `port` in the `check` block
