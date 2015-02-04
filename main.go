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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type collectdMetric struct {
	Values         []float64
	Dstypes        []string
	Dsnames        []string
	Time           float64
	Interval       float64
	Host           string
	Plugin         string
	PluginInstance string `json:"plugin_instance"`
	Type           string
	TypeInstance   string `json:"type_instance"`
}

var (
	addr     = flag.String("listen-address", ":6060", "The address to listen on for HTTP requests.")
	lastPush = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "collectd_last_push",
			Help: "Unixtime the collectd exporter was last pushed to.",
		},
	)
)

func metricName(m collectdMetric, dstype string, dsname string) string {
	result := "collectd"
	if m.Plugin != m.Type && !strings.HasPrefix(m.Type, m.Plugin+"_") {
		result += "_" + m.Plugin
	}
	result += "_" + m.Type
	if dsname != "value" {
		result += "_" + dsname
	}
	if dstype == "counter" {
		result += "_total"
	}
	return result
}

func metricHelp(m collectdMetric, dstype string, dsname string) string {
	return fmt.Sprintf("Collectd Metric Plugin: '%s' Type: '%s' Dstype: '%s' Dsname: '%s'",
		m.Plugin, m.Type, dstype, dsname)
}

type collectdSample struct {
	Name    string
	Labels  map[string]string
	Help    string
	Value   float64
	Gauge   bool
	Expires time.Time
}

type collectdSampleLabelset struct {
	Name           string
	Instance       string
	Type           string
	Plugin         string
	PluginInstance string
}

type CollectdCollector struct {
	samples map[collectdSampleLabelset]*collectdSample
	mu      *sync.Mutex
	ch      chan *collectdSample
}

func newCollectdCollector() *CollectdCollector {
	c := &CollectdCollector{
		ch:      make(chan *collectdSample, 0),
		mu:      &sync.Mutex{},
		samples: map[collectdSampleLabelset]*collectdSample{},
	}
	go c.processSamples()
	return c
}

func (c *CollectdCollector) collectdPost(w http.ResponseWriter, r *http.Request) {
	var postedMetrics []collectdMetric
	err := json.NewDecoder(r.Body).Decode(&postedMetrics)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	now := time.Now()
	lastPush.Set(float64(now.UnixNano()) / 1e9)
	for _, metric := range postedMetrics {
		for i, value := range metric.Values {
			name := metricName(metric, metric.Dstypes[i], metric.Dsnames[i])
			help := metricHelp(metric, metric.Dstypes[i], metric.Dsnames[i])
			labels := prometheus.Labels{}
			if metric.PluginInstance != "" {
				labels[metric.Plugin] = metric.PluginInstance
			}
			if metric.TypeInstance != "" {
				if metric.PluginInstance == "" {
					labels[metric.Plugin] = metric.TypeInstance
				} else {
					labels["type"] = metric.TypeInstance
				}
			}
			labels["instance"] = metric.Host
			c.ch <- &collectdSample{
				Name:    name,
				Labels:  labels,
				Help:    help,
				Value:   value,
				Gauge:   metric.Dstypes[i] != "counter",
				Expires: now.Add(time.Duration(metric.Interval) * time.Second * 2),
			}
		}
	}
}

func (c *CollectdCollector) processSamples() {
	ticker := time.NewTicker(time.Minute).C
	for {
		select {
		case sample := <-c.ch:
			labelset := &collectdSampleLabelset{
				Name: sample.Name,
			}
			for k, v := range sample.Labels {
				switch k {
				case "instance":
					labelset.Instance = v
				case "type":
					labelset.Type = v
				default:
					labelset.Plugin = k
					labelset.PluginInstance = v
				}
			}
			c.mu.Lock()
			c.samples[*labelset] = sample
			c.mu.Unlock()
		case <-ticker:
			// Garbage collect expired samples.
			now := time.Now()
			c.mu.Lock()
			for k, sample := range c.samples {
				if now.After(sample.Expires) {
					delete(c.samples, k)
				}
			}
			c.mu.Unlock()
		}
	}
}

// Implements Collector.
func (c CollectdCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- lastPush
	c.mu.Lock()
	samples := c.samples
	c.mu.Unlock()
	now := time.Now()
	for _, sample := range samples {
		if now.After(sample.Expires) {
			continue
		}
		if sample.Gauge {
			gauge := prometheus.NewGauge(
				prometheus.GaugeOpts{
					Name:        sample.Name,
					Help:        sample.Help,
					ConstLabels: sample.Labels})
			gauge.Set(sample.Value)
			ch <- gauge
		} else {
			counter := prometheus.NewCounter(
				prometheus.CounterOpts{
					Name:        sample.Name,
					Help:        sample.Help,
					ConstLabels: sample.Labels})
			counter.Set(sample.Value)
			ch <- counter
		}
	}
}

// Implements Collector.
func (c CollectdCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- lastPush.Desc()
}

func main() {
	flag.Parse()
	http.Handle("/metrics", prometheus.Handler())
	c := newCollectdCollector()
	http.HandleFunc("/collectd-post", c.collectdPost)
	prometheus.MustRegister(c)
	http.ListenAndServe(*addr, nil)
}
