# Collectd Exporter 


An exporter for [collectd](https://collectd.org/). It accepts collectd's
[binary network protocol](https://collectd.org/wiki/index.php/Binary_protocol)
as sent by collectd's
[network plugin](https://collectd.org/wiki/index.php/Plugin:Network) and
metrics in JSON format via HTTP POST as sent by collectd's
[write_http plugin](https://collectd.org/wiki/index.php/Plugin:Write_HTTP),
and transforms and exposes them for consumption by Prometheus.

This is useful for exporting metrics from existing collectd setups, as
well as for metrics which are not covered by the core Prometheus exporters such
as the [Node Exporter](https://github.com/prometheus/node_exporter).

## AWS EC2 Specifics

This exporter is specially built for reporting AWS EC2 instance metrics.

The exporter will periodically call the EC2 Introspection API as well as EC2 Describe Tags API to pull the latest EC2 metadata and tag information.

These metadata and tag information will then first be sanitized according to the Prometheus metric names and labels [naming convention](https://prometheus.io/docs/concepts/data_model/) and made part of ingested data as values for labels.

More specifically, the following labels will be added to each metric data (in addition to existing labels inserted by original  [Collectd Exporter](https://github.com/prometheus/collectd_exporter)).

| Label         | Value 
|---------------|-------------|
| Application   | value from tag key: Application
| Environment   | value from tag key: Environment
| Stack_Role    | value from tag key: Stack and Role
| Host          | ec2 instance id |


The frequency of update can be controlled by specifying `metadata.refresh.period.min=1` in the command line when starting the exporter. In this exmaple, frequency is every 1 min. 



## Binary network protocol

collectd's *network plugin* uses a lightweight binary protocol to send metrics
from one instance to another. To consume these packets with
*collectd_exporter*, first configure collectd to send these metrics to the
appropriate address:

```
LoadPlugin network
<Plugin network>
  Server "localhost" "25826"
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
  <Node "collectd_exporter">
    URL "http://localhost:9103/collectd-post"
    Format "JSON"
    StoreRates false
  </Node>
</Plugin>
```

To change the path of the end-point, use the `-web.collectd-push-path` command
line option. To disable this functionality altogether, use
`-web.collectd-push-path=""`.


## Build

To be able to build from source code, you need to have a working Go environment with [verion 1.7 or greater](https://golang.org/doc/install) installed.

Follow the steps here to build using `make`:

    $ mkdir -p $GOPATH/src/tmobile/collectd_exporter
    $ cd $GOPATH/src/tmobile/collectd_exporter
    $ git clone git@github.com:dev9com/collectd_exporter.git
    $ cd collectd_exporter
    $ make all

The Makefile provides several targets:

  * *build*: build the binary
  * *format*: format the source code
  * *docker*: build a docker container for the current `HEAD`
  * all: execute `format`, `build`, `docker` goals.

Alternatively, you can also run the following command to build the source.

```
$ go build -o collectd-exporter
```


### Build Artifact

* A executable binary `collectd_exporter` should exist after the build
* A Docker image `tmobile/collectd-exporter` should exist in your local docker registry.

### Note
* Depends on the platform you are using to build the binary, the artifact is only capable of running on the platform you build it with.

* Use the `go env` command to find out which `architecture` and `os` you are building with. The following configuration is for buidling to run on Linux platform. See https://github.com/golang/go/blob/master/src/go/build/syslist.go for the list of supported OS and CPU architecture.

	`GOARCH="amd64"`
	
	`GOOS="linux"`



## Run

To run the executable binary from the command line

```$./collectd_exporter```

To run the Docker container

```docker run -d  -p 9103:9103 -p 25826:25826/udp tmobile/collectd-exporter -collectd.listen-address=":25826" -metadata.refresh.period.min="1"```


## Jenkins Server and Jenkinsfile
* Alternatively, if you have a Jenkins server which supports Pipeline, you can build the artifact using the Jenkinsfile.

* See [Provisioning Jenkins Server in AWS](./docs/ProvisioningJenkinsServerinAWS.pdf) for an example of setting up a AWS EC2 instance and installing Jenkins server.

* See [Jenkins Job For Building CollectD Exporter Using Jenkinsfile](./docs/JenkinsJobForBuildingCollectDExporterUsingJenkinsfile.pdf) for setting up Jenkins job.

* See [config.xml](./jenkins/config.xml) for an sample of Jenkins job configuration file. You would need to use the actual credentials id configured on the Jenkins server.





