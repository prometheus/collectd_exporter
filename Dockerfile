ARG ARCH="amd64"
ARG OS="linux"
FROM  quay.io/prometheus/busybox-${OS}-${ARCH}:latest
LABEL maintainer="The Prometheus Authors <prometheus-developers@googlegroups.com>"

ARG ARCH="amd64"
ARG OS="linux"
COPY .build/${OS}-${ARCH}/collectd_exporter /bin/collectd_exporter

EXPOSE      9103
ENTRYPOINT  [ "/bin/collectd_exporter" ]
