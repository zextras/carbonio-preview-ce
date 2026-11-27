// Generated from the packaged template on every upgrade and setup:
// local edits to this file are overwritten.
// To change the values, set them in /etc/zextras/service-discover/carbonio-preview.env
// (KEY=value lines; keys and defaults are printed at the end of carbonio-preview setup),
// then run carbonio-preview setup.

services {
  check {
    http     = "http://127.78.0.6:10000/health/live/"
    method   = "GET"
    timeout  = "{{ or (env "CHECK_TIMEOUT") "1s" }}"
    interval = "{{ or (env "CHECK_INTERVAL") "5s" }}"
  }
  connect {
    sidecar_service {
      proxy {
        local_service_address = "127.78.0.6"
        upstreams = [
          {
            destination_name   = "carbonio-storages"
            local_bind_address = "127.78.0.6"
            local_bind_port    = 20000
          },
          {
            destination_name   = "carbonio-docs-editor"
            local_bind_address = "127.78.0.6"
            local_bind_port    = 20001
          },
          {
            destination_name   = "carbonio-preview-db"
            local_bind_address = "127.78.0.6"
            local_bind_port    = 20003
          }
        ]
      }
    }
  }

  name = "carbonio-preview"
  port = 10000
}
