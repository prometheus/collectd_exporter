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
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"collectd.org/api"
	"github.com/prometheus/client_golang/prometheus"
)

// Timeout specifies the number of iterations after which a metric times out,
// i.e. becomes stale and is removed from collectdCollector.valueLists. It is
// modeled and named after the top-level "Timeout" setting of collectd.
const Timeout = 2

var (
	listeningAddress = flag.String("web.listen-address", ":9103", "Address on which to expose metrics and web interface.")
	metricsPath      = flag.String("web.telemetry-path", "/metrics", "Path under which to expose Prometheus metrics.")
	collectdPostPath = flag.String("web.collectd-push-path", "/collectd-post", "Path under which to accept POST requests from collectd.")
	lastPush         = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "collectd_last_push_timestamp_seconds",
			Help: "Unix timestamp of the last received collectd metrics push in seconds.",
		},
	)
)

// newDesc converts one data source of a value list to a Prometheus description.
func newDesc(vl api.ValueList, index int) *prometheus.Desc {
	var name string
	if vl.Plugin == vl.Type || strings.HasPrefix(vl.Type, vl.Plugin+"_") {
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

	help := fmt.Sprintf("Collectd exporter: '%s' Type: '%s' Dstype: '%T' Dsname: '%s'",
		vl.Plugin, vl.Type, vl.Values[index], vl.DSName(index))

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

	return prometheus.NewDesc(name, help, []string{}, labels)
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

	var valueLists []api.ValueList
	if err := json.Unmarshal(data, &valueLists); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	for _, vl := range valueLists {
		c.Write(vl)
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
				validUntil := vl.Time.Add(Timeout * vl.Interval)
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
		validUntil := vl.Time.Add(Timeout * vl.Interval)
		if validUntil.Before(now) {
			continue
		}

		for i := range vl.Values {
			m, err := newMetric(vl, i)
			if err != nil {
				log.Printf("newMetric: %v", err)
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
func (c collectdCollector) Write(vl api.ValueList) error {
	lastPush.Set(float64(time.Now().UnixNano()) / 1e9)
	c.ch <- vl

	return nil
}

func main() {
	flag.Parse()

	c := newCollectdCollector()
	prometheus.MustRegister(c)

	http.Handle(*metricsPath, prometheus.Handler())
	http.HandleFunc(*collectdPostPath, c.collectdPost)
	http.ListenAndServe(*listeningAddress, nil)
}
