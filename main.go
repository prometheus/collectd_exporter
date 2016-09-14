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
	"flag"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"collectd.org/api"
	"collectd.org/network"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/log"
	"github.com/prometheus/common/version"
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
	metricsPath      = flag.String("web.telemetry-path", "/metrics", "Path under which to expose Prometheus metrics.")
	collectdPostPath = flag.String("web.collectd-push-path", "/collectd-post", "Path under which to accept POST requests from collectd.")
	lastPush         = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "collectd_last_push_timestamp_seconds",
			Help: "Unix timestamp of the last received collectd metrics push in seconds.",
		},
	)
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
	case api.Counter:
		name += "_total"
	}

	return name
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
