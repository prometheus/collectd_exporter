FROM        quay.io/prometheus/busybox:latest
MAINTAINER  The Prometheus Authors <prometheus-developers@googlegroups.com>

COPY collectd_exporter /bin/collectd_exporter

EXPOSE      9103
ENTRYPOINT  [ "/bin/collectd_exporter" ]
