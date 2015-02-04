# Collectd Exporter

An exporter for [collectd](https://collectd.org/). It accepts collectd metrics
in JSON format via HTTP POST (as sent by the
[write_http](https://collectd.org/wiki/index.php/Plugin:Write_HTTP) collectd
plugin) and transforms and exposes them for consumption by Prometheus.

This exporter is useful for exporting metrics from existing collectd setups, as
well as for metrics which are not covered by the core Prometheus exporters such
as the [Node Exporter](https://github.com/prometheus/node_exporter).

## Collectd configuration

Configure collectd to push to the collectd exporter over HTTP:

```
LoadPlugin write_http
<Plugin write_http>
  <URL "http://localhost:6060/collectd-post">
    Format "JSON"
    StoreRates false
  </URL>
</Plugin>
```
