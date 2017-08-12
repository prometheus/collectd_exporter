# Collectd Exporter [![Build Status](https://travis-ci.org/prometheus/collectd_exporter.svg)][travis]

[![CircleCI](https://circleci.com/gh/prometheus/collectd_exporter/tree/master.svg?style=shield)][circleci]
[![Docker Repository on Quay](https://quay.io/repository/prometheus/collectd-exporter/status)][quay]
[![Docker Pulls](https://img.shields.io/docker/pulls/prom/collectd-exporter.svg?maxAge=604800)][hub]

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

Then start *collectd_exporter* with `--collectd.listen-address=":25826"` to
start consuming and exporting these metrics.

## JSON format

collectd's *write_http plugin* is able to send metrics via HTTP POST requests.
*collectd_exporter* serves an appropriate end-point which accepts, parses and
exports the metrics. First, configure collectd to send these metrics to the
HTTP end-point:

```
LoadPlugin write_http
<Plugin write_http>
  <Node "collectd_exporter">
    URL "http://localhost:9103/collectd-post"
    Format "JSON"
    StoreRates false
  </Node>
</Plugin>
```

To change the path of the end-point, use the `--web.collectd-push-path` command
line option. To disable this functionality altogether, use
`--web.collectd-push-path=""`.

## Using Docker

You can deploy this exporter using the [prom/collectd-exporter][hub] Docker image.
You will need to map the collectd port from the container to the host, remembering
that this is a UDP port.

For example:

```bash
docker pull prom/collectd-exporter

docker run -d -p 9103:9103 -p 25826:25826/udp prom/collectd-exporter --collectd.listen-address=":25826"
```


[circleci]: https://circleci.com/gh/prometheus/collectd_exporter
[hub]: https://hub.docker.com/r/prom/collectd-exporter/
[travis]: https://travis-ci.org/prometheus/collectd_exporter
[quay]: https://quay.io/repository/prometheus/collectd-exporter
