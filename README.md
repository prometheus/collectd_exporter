# Collectd Exporter

An exporter for [collectd](https://collectd.org/), pushed to by the [write_http](https://collectd.org/wiki/index.php/Plugin:Write_HTTP) plugin.

This is provided for machine metrics that core Prometheus exporters and
instrumentation such as the [Node Exporter](https://github.com/prometheus/node_exporter)
don't cover.

## Collectd Configuration

Configure collectd to push to the collectd exporter over HTTP:

```
LoadPlugin write_http
<Plugin write_http>
  <URL "http://localhost:6060/collectd-post">
    Format "JSON"
  </URL>
</Plugin>
```
