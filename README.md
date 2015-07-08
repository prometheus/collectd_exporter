# Collectd Exporter

An exporter for [collectd](https://collectd.org/). It accepts collectd's
[binary network protocol](https://collectd.org/wiki/index.php/Binary_protocol)
as sent by collectd's
[network plugin](https://collectd.org/wiki/index.php/Plugin:Network) and
metrics in JSON format via HTTP POST as sent by collectd's
[write_http plugin](https://collectd.org/wiki/index.php/Plugin:Write_HTTP),
and transforms and exposes them for consumption by Prometheus.

This exporter is useful for exporting metrics from existing collectd setups, as
well as for metrics which are not covered by the core Prometheus exporters such
as the [Node Exporter](https://github.com/prometheus/node_exporter).

## Binary network protocol

collectd's *network plugin* uses a lightweight binary protocol to send metrics
from one instance to another. To consume these packets with
*collectd_exporter*, first configure collectd to send these metrics to the
appropriate address:

```
LoadPlugin network
<Plugin network>
  Server "prometheus.example.com" "25826"
</Plugin>
```

Then start *collectd_exporter* with `-collectd.listen-address=":25826"` to
start consuming and exporting these metrics.

## JSON format

collectd's *write_http plugin* is able to send metrics via HTTP POST requests.
*collectd_exporter* serves an appropriate end-point which accepts, parses and
exports the metrics. First, configure collectd to send these metrics to the
HTTP end-point:

```
LoadPlugin write_http
<Plugin write_http>
  <URL "http://localhost:9103/collectd-post">
    Format "JSON"
    StoreRates false
  </URL>
</Plugin>
```

To change the path of the end-point, use the `-web.collectd-push-path` command
line option. To disable this functionality altogether, use
`-web.collectd-push-path=""`.
