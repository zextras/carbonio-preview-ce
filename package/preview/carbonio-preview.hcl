services {
  check {
    http = "http://127.78.0.6:10000/health/ready/",
    method = "GET",
    timeout = "1s"
    interval = "5s"
  }
  connect {
    sidecar_service {
      proxy {
        local_service_address = "127.78.0.6"
        upstreams = [
          {
            destination_name = "carbonio-storages"
            local_bind_address = "127.78.0.6"
            local_bind_port = 20000
          },
          {
            destination_name = "carbonio-docs-editor"
            local_bind_address = "127.78.0.6"
            local_bind_port = 20001
          }
        ]
      }
    }
  }
  
  name = "carbonio-preview"
  port = 10000
}
