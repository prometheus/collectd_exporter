// Copyright 2015 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"collectd.org/api"
	"collectd.org/network"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// timeout specifies the number of iterations after which a metric times out,
// i.e. becomes stale and is removed from collectdCollector.valueLists. It is
// modeled and named after the top-level "Timeout" setting of collectd.
const timeout = 2

var (
	showVersion      = flag.Bool("version", false, "Print version information.")
	listenAddress    = flag.String("web.listen-address", ":9103", "Address on which to expose metrics and web interface.")
	collectdAddress  = flag.String("collectd.listen-address", "", "Network address on which to accept collectd binary network packets, e.g. \":25826\".")
	collectdBuffer   = flag.Int("collectd.udp-buffer", 0, "Size of the receive buffer of the socket used by collectd binary protocol receiver.")
	collectdAuth     = flag.String("collectd.auth-file", "", "File mapping user names to pre-shared keys (passwords).")
	collectdSecurity = flag.String("collectd.security-level", "None", "Minimum required security level for accepted packets. Must be one of \"None\", \"Sign\" and \"Encrypt\".")
	collectdTypesDB  = flag.String("collectd.typesdb-file", "", "Collectd types.db file for datasource names mapping. Needed only if using a binary network protocol.")
	metricsPath      = flag.String("web.telemetry-path", "/metrics", "Path under which to expose Prometheus metrics.")
	collectdPostPath = flag.String("web.collectd-push-path", "/collectd-post", "Path under which to accept POST requests from collectd.")
	lastPush         = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "collectd_last_push_timestamp_seconds",
			Help: "Unix timestamp of the last received collectd metrics push in seconds.",
		},
	)

	metadataRefreshPeriod = flag.Int("metadata.refresh.period.min", 1, "refresh period in mins for retrieving metadata")
)

// newName converts one data source of a value list to a string representation.
func newName(vl api.ValueList, index int) string {
	var name string
	if vl.Plugin == vl.Type {
		name = "collectd_" + vl.Type
	} else {
		name = "collectd_" + vl.Plugin + "_" + vl.Type
	}
	if dsname := vl.DSName(index); dsname != "value" {
		name += "_" + dsname
	}
	switch vl.Values[index].(type) {
	case api.Counter, api.Derive:
		name += "_total"
	}

	return name
}

// newLabels converts the plugin and type instance of vl to a set of prometheus.Labels.
func newLabels(vl api.ValueList, md metadata) prometheus.Labels {
	labels := prometheus.Labels{}

	if _, ok := md.tags["Application"]; ok {
		labels["Application"] = md.tags["Application"]
	}

	if _, ok := md.tags["Environment"]; ok {
		labels["Environment"] = md.tags["Environment"]
	}

	stack_value, stack_ok := md.tags["Stack"]
	role_value, role_ok := md.tags["Role"]

	if stack_ok && role_ok {
		labels["Stack_Role"] = stack_value + "_" + role_value
	}

	labels["Host"] = md.instanceId
	labels["instance"] = vl.Host

	if vl.PluginInstance != "" {
		labels[vl.Plugin] = vl.PluginInstance
	}

	if vl.TypeInstance != "" {
		if vl.PluginInstance == "" {
			labels[vl.Plugin] = vl.TypeInstance
		} else {
			labels["type"] = vl.TypeInstance
		}
	}

	log.Debugf("DSNames: %v, Values: %v, Type: %v, labels: %v", vl.DSNames, vl.Values, vl.Type, labels)

	return labels
}

func sanitize(input string) string {
	re, _ := regexp.Compile("-")
	actual := string(re.ReplaceAll([]byte(input), []byte("_")))
	return strings.ToLower(actual)
}

// newDesc converts one data source of a value list to a Prometheus description.
func newDesc(vl api.ValueList, index int, md metadata) *prometheus.Desc {
	help := fmt.Sprintf("Collectd exporter: '%s' Type: '%s' Dstype: '%T' Dsname: '%s'",
		vl.Plugin, vl.Type, vl.Values[index], vl.DSName(index))

	return prometheus.NewDesc(newName(vl, index), help, []string{}, newLabels(vl, md))
}

// newMetric converts one data source of a value list to a Prometheus metric.
func newMetric(vl api.ValueList, index int, md metadata) (prometheus.Metric, error) {
	var value float64
	var valueType prometheus.ValueType

	switch v := vl.Values[index].(type) {
	case api.Gauge:
		value = float64(v)
		valueType = prometheus.GaugeValue
	case api.Derive:
		value = float64(v)
		valueType = prometheus.CounterValue
	case api.Counter:
		value = float64(v)
		valueType = prometheus.CounterValue
	default:
		return nil, fmt.Errorf("unknown value type: %T", v)
	}

	return prometheus.NewConstMetric(newDesc(vl, index, md), valueType, value)
}

type collectdCollector struct {
	ch         chan api.ValueList
	valueLists map[string]api.ValueList
	mu         *sync.Mutex
	md         metadata
}

type metadata struct {
	tags             map[string]string
	instanceId       string
	privateIpAddress string
}

func newCollectdCollector() *collectdCollector {
	c := &collectdCollector{
		ch:         make(chan api.ValueList, 0),
		valueLists: make(map[string]api.ValueList),
		mu:         &sync.Mutex{},
		md: metadata{
			tags: make(map[string]string),
		},
	}
	go c.processSamples()
	return c
}

func (c *collectdCollector) CollectdPost(w http.ResponseWriter, r *http.Request) {
	log.Infof("processing %v request", *collectdPostPath)

	data, err := ioutil.ReadAll(r.Body)

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Error("tpp error:", err.Error())
		return
	}

	var valueLists []*api.ValueList

	if err := json.Unmarshal(data, &valueLists); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Error("unmarshal error:", err.Error())
		return
	}

	for _, vl := range valueLists {
		log.Debugf("host:%v, values:%v, DSNames:%v, time:%v, type:%v, plugins:%v, interval:%v, identifier:%v",
			vl.Host, vl.Values, vl.DSNames, vl.Time, vl.Type, vl.Plugin, vl.Interval, vl.Identifier)
		c.Write(r.Context(), vl)
	}
}

func (c *collectdCollector) processSamples() {
	log.Infoln("processSamples")

	ticker := time.NewTicker(time.Minute).C

	var duration = time.Duration(*metadataRefreshPeriod) * time.Minute

	log.Infof("metadata refresh period: %v min", duration.Minutes())

	tag_ticker := time.NewTicker(duration).C

	for {
		select {
		case vl := <-c.ch:
			id := vl.Identifier.String()
			c.mu.Lock()
			c.valueLists[id] = vl
			c.mu.Unlock()

		case <-ticker:
		// Garbage collect expired value lists.
			now := time.Now()
			c.mu.Lock()
			for id, vl := range c.valueLists {
				validUntil := vl.Time.Add(timeout * vl.Interval)
				if validUntil.Before(now) {
					delete(c.valueLists, id)
				}
			}
			c.mu.Unlock()

		case <-tag_ticker:
			c.mu.Lock()
			go refreshMetadata(c)
			c.mu.Unlock()
		}
	}
}

// Collect implements prometheus.Collector.
func (c collectdCollector) Collect(ch chan<- prometheus.Metric) {
	log.Infof("processing %v request", *metricsPath)

	ch <- lastPush

	c.mu.Lock()
	valueLists := make([]api.ValueList, 0, len(c.valueLists))
	for _, vl := range c.valueLists {
		valueLists = append(valueLists, vl)
	}
	c.mu.Unlock()

	now := time.Now()

	for _, vl := range valueLists {
		validUntil := vl.Time.Add(timeout * vl.Interval)
		if validUntil.Before(now) {
			continue
		}

		for i := range vl.Values {
			m, err := newMetric(vl, i, c.md)
			if err != nil {
				log.Errorf("newMetric: %v", err)
				continue
			}

			ch <- m
		}
	}
}

// Describe implements prometheus.Collector.
func (c collectdCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- lastPush.Desc()
}

// Write writes "vl" to the collector's channel, to be (asynchronously)
// processed by processSamples(). It implements api.Writer.
func (c collectdCollector) Write(_ context.Context, vl *api.ValueList) error {
	lastPush.Set(float64(time.Now().UnixNano()) / 1e9)
	c.ch <- *vl

	return nil
}

func startCollectdServer(ctx context.Context, w api.Writer) {
	if *collectdAddress == "" {
		return
	}

	srv := network.Server{
		Addr:   *collectdAddress,
		Writer: w,
	}

	if *collectdAuth != "" {
		srv.PasswordLookup = network.NewAuthFile(*collectdAuth)
	}

	if *collectdTypesDB != "" {
		file, err := os.Open(*collectdTypesDB)
		if err != nil {
			log.Fatalf("Can't open types.db file %s", *collectdTypesDB)
		}
		defer file.Close()

		typesDB, err := api.NewTypesDB(file)
		if err != nil {
			log.Fatalf("Error in parsing types.db file %s", *collectdTypesDB)
		}
		srv.TypesDB = typesDB
	}

	switch strings.ToLower(*collectdSecurity) {
	case "", "none":
		srv.SecurityLevel = network.None
	case "sign":
		srv.SecurityLevel = network.Sign
	case "encrypt":
		srv.SecurityLevel = network.Encrypt
	default:
		log.Fatalf("Unknown security level %q. Must be one of \"None\", \"Sign\" and \"Encrypt\".", *collectdSecurity)
	}

	laddr, err := net.ResolveUDPAddr("udp", *collectdAddress)
	if err != nil {
		log.Fatalf("Failed to resolve binary protocol listening UDP address %q: %v", *collectdAddress, err)
	}

	if laddr.IP != nil && laddr.IP.IsMulticast() {
		srv.Conn, err = net.ListenMulticastUDP("udp", nil, laddr)
	} else {
		srv.Conn, err = net.ListenUDP("udp", laddr)
	}
	if err != nil {
		log.Fatalf("Failed to create a socket for a binary protocol server: %v", err)
	}
	if *collectdBuffer >= 0 {
		if err = srv.Conn.SetReadBuffer(*collectdBuffer); err != nil {
			log.Fatalf("Failed to adjust a read buffer of the socket: %v", err)
		}
	}

	go func() {
		log.Fatal(srv.ListenAndWrite(ctx))
	}()
}

func init() {
	prometheus.MustRegister(version.NewCollector("collectd_exporter"))
}

func refreshMetadata(c *collectdCollector) {
	log.Info("refresh metadata")

	var expectedTags map[string]int

	expectedTags = make(map[string]int)
	expectedTags["Name"] = 1
	expectedTags["Application"] = 1
	expectedTags["Environment"] = 1
	expectedTags["Stack"] = 1
	expectedTags["Role"] = 1

	log.Debugf("expected tags:", expectedTags)

	// retrieve ec2 instance id
	resp, err := http.Get("http://169.254.169.254/latest/meta-data/instance-id")

	if err != nil {
		log.Errorf("Failed to call introspection api to retrieve instance id, %v", err)
		return
	}

	defer resp.Body.Close()

	data, err := ioutil.ReadAll(resp.Body)

	c.md.instanceId = string(data)

	log.Infof("instance-id: %v", c.md.instanceId)

	// retrieve ec2 private ip address
	ip_resp, ip_err := http.Get("http://169.254.169.254/latest/meta-data/local-ipv4")

	if ip_err != nil {
		log.Errorf("Failed to call introspection api to retrieve private IP address, %v", ip_err)
		return
	}

	defer ip_resp.Body.Close()

	ip_data, _ := ioutil.ReadAll(ip_resp.Body)

	c.md.privateIpAddress = string(ip_data)

	log.Infof("private-ip: %v", c.md.privateIpAddress)

	// retrieve ec2 AZ
	az_resp, az_err := http.Get("http://169.254.169.254/latest/meta-data/placement/availability-zone/")

	if az_err != nil {
		log.Errorf("Failed to call introspection api to retrieve AZ, %v", az_err)
		return
	}

	defer az_resp.Body.Close()

	az_data, az_err := ioutil.ReadAll(az_resp.Body)

	var az string = string(az_data)

	var region string = az[0 : len(az)-1]

	log.Infof("region: %v", region)

	// ec2 api call
	session, err := session.NewSession()

	if err != nil {
		log.Errorf("failed to create session %v\n", err)
		return
	}

	service := ec2.New(session, &aws.Config{Region: aws.String(region)})

	params := &ec2.DescribeTagsInput{
		Filters: []*ec2.Filter{
			{
				Name: aws.String("resource-id"),
				Values: []*string{
					aws.String(c.md.instanceId),
				},
			},
		},
	}

	describeTagsRes, err := service.DescribeTags(params)

	if err != nil {
		log.Errorf("failed to call ec2.describe_tags %v\n", err)
		return
	}

	for _, tag := range describeTagsRes.Tags {
		if _, ok := expectedTags[*tag.Key]; ok {
			//var s_key string = sanitize(*tag.Key)
			//var s_value string = sanitize(*tag.Value)

			c.md.tags[*tag.Key] = *tag.Value

			log.Infof("tag-key:%v, tag-value: %v", *tag.Key, c.md.tags[*tag.Key])
		}
	}

}

func main() {
	flag.Parse()

	if *showVersion {
		fmt.Fprintln(os.Stdout, version.Print("collectd_exporter"))
		os.Exit(0)
	}

	log.Infoln("Starting collectd_exporter", version.Info())
	log.Infoln("Build context", version.BuildContext())

	c := newCollectdCollector()
	prometheus.MustRegister(c)

	go refreshMetadata(c)

	startCollectdServer(context.Background(), c)

	if *collectdPostPath != "" {
		http.HandleFunc(*collectdPostPath, c.CollectdPost)
	}

	http.Handle(*metricsPath, prometheus.Handler())
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>
             <head><title>Collectd Exporter</title></head>
             <body>
             <h1>Collectd Exporter</h1>
             <p><a href='` + *metricsPath + `'>Metrics</a></p>
             </body>
             </html>`))
	})

	log.Infoln("Listening on", *listenAddress)
	log.Fatal(http.ListenAndServe(*listenAddress, nil))
}
