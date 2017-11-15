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
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"collectd.org/api"
	"collectd.org/network"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
	"gopkg.in/alecthomas/kingpin.v2"
)

// timeout specifies the number of iterations after which a metric times out,
// i.e. becomes stale and is removed from collectdCollector.valueLists. It is
// modeled and named after the top-level "Timeout" setting of collectd.
const timeout = 2

var (
	listenAddress    = kingpin.Flag("web.listen-address", "Address on which to expose metrics and web interface.").Default(":9103").String()
	collectdAddress  = kingpin.Flag("collectd.listen-address", "Network address on which to accept collectd binary network packets, e.g. \":25826\".").Default("").String()
	collectdBuffer   = kingpin.Flag("collectd.udp-buffer", "Size of the receive buffer of the socket used by collectd binary protocol receiver.").Default("0").Int()
	collectdAuth     = kingpin.Flag("collectd.auth-file", "File mapping user names to pre-shared keys (passwords).").Default("").String()
	collectdSecurity = kingpin.Flag("collectd.security-level", "Minimum required security level for accepted packets. Must be one of \"None\", \"Sign\" and \"Encrypt\".").Default("None").String()
	collectdTypesDB  = kingpin.Flag("collectd.typesdb-file", "Collectd types.db file for datasource names mapping. Needed only if using a binary network protocol.").Default("").String()
	metricsPath      = kingpin.Flag("web.telemetry-path", "Path under which to expose Prometheus metrics.").Default("/metrics").String()
	collectdPostPath = kingpin.Flag("web.collectd-push-path", "Path under which to accept POST requests from collectd.").Default("/collectd-post").String()
	lastPush         = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "collectd_last_push_timestamp_seconds",
			Help: "Unix timestamp of the last received collectd metrics push in seconds.",
		},
	)
	metric_name_re = regexp.MustCompile("[^a-zA-Z0-9_:]")
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

	return metric_name_re.ReplaceAllString(name, "_")
}

// newLabels converts the plugin and type instance of vl to a set of prometheus.Labels.
func newLabels(vl api.ValueList) prometheus.Labels {
	labels := prometheus.Labels{}
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
	labels["instance"] = vl.Host

	return labels
}

// newDesc converts one data source of a value list to a Prometheus description.
func newDesc(vl api.ValueList, index int) *prometheus.Desc {
	help := fmt.Sprintf("Collectd exporter: '%s' Type: '%s' Dstype: '%T' Dsname: '%s'",
		vl.Plugin, vl.Type, vl.Values[index], vl.DSName(index))

	return prometheus.NewDesc(newName(vl, index), help, []string{}, newLabels(vl))
}

// newMetric converts one data source of a value list to a Prometheus metric.
func newMetric(vl api.ValueList, index int) (prometheus.Metric, error) {
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

	return prometheus.NewConstMetric(newDesc(vl, index), valueType, value)
}

type collectdCollector struct {
	ch         chan api.ValueList
	valueLists map[string]api.ValueList
	mu         *sync.Mutex
}

func newCollectdCollector() *collectdCollector {
	c := &collectdCollector{
		ch:         make(chan api.ValueList, 0),
		valueLists: make(map[string]api.ValueList),
		mu:         &sync.Mutex{},
	}
	go c.processSamples()
	return c
}

func (c *collectdCollector) collectdPost(w http.ResponseWriter, r *http.Request) {
	data, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var valueLists []*api.ValueList
	if err := json.Unmarshal(data, &valueLists); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for _, vl := range valueLists {
		c.Write(r.Context(), vl)
	}
}

func (c *collectdCollector) processSamples() {
	ticker := time.NewTicker(time.Minute).C
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
		}
	}
}

// Collect implements prometheus.Collector.
func (c collectdCollector) Collect(ch chan<- prometheus.Metric) {
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
			m, err := newMetric(vl, i)
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

	proto := "udp"
	if strings.HasPrefix(*collectdAddress, "[") {
		proto = "udp6"
	}
	laddr, err := net.ResolveUDPAddr(proto, *collectdAddress)
	if err != nil {
		log.Fatalf("Failed to resolve binary protocol listening UDP address %q: %v", *collectdAddress, err)
	}

	if laddr.IP != nil && laddr.IP.IsMulticast() {
		srv.Conn, err = net.ListenMulticastUDP(proto, nil, laddr)
	} else {
		srv.Conn, err = net.ListenUDP(proto, laddr)
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

func main() {
	log.AddFlags(kingpin.CommandLine)
	kingpin.Version(version.Print("collectd_exporter"))
	kingpin.HelpFlag.Short('h')
	kingpin.Parse()

	log.Infoln("Starting collectd_exporter", version.Info())
	log.Infoln("Build context", version.BuildContext())

	c := newCollectdCollector()
	prometheus.MustRegister(c)

	startCollectdServer(context.Background(), c)

	if *collectdPostPath != "" {
		http.HandleFunc(*collectdPostPath, c.collectdPost)
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
