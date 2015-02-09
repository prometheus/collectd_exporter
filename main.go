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
	return fmt.Sprintf("Collectd exporter: '%s' Type: '%s' Dstype: '%s' Dsname: '%s'",
		m.Plugin, m.Type, dstype, dsname)
}

func metricType(dstype string) prometheus.ValueType {
	if dstype == "counter" {
		return prometheus.CounterValue
	}
	return prometheus.GaugeValue
}

type collectdSample struct {
	Name    string
	Labels  map[string]string
	Help    string
	Value   float64
	Type    prometheus.ValueType
	Expires time.Time
}

type collectdSampleLabelset struct {
	Name           string
	Instance       string
	Type           string
	Plugin         string
	PluginInstance string
}

type collectdCollector struct {
	samples map[collectdSampleLabelset]*collectdSample
	mu      *sync.Mutex
	ch      chan *collectdSample
}

func newCollectdCollector() *collectdCollector {
	c := &collectdCollector{
		ch:      make(chan *collectdSample, 0),
		mu:      &sync.Mutex{},
		samples: map[collectdSampleLabelset]*collectdSample{},
	}
	go c.processSamples()
	return c
}

func (c *collectdCollector) collectdPost(w http.ResponseWriter, r *http.Request) {
	var postedMetrics []collectdMetric
	err := json.NewDecoder(r.Body).Decode(&postedMetrics)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	now := time.Now()
	lastPush.Set(float64(now.UnixNano()) / 1e9)
	for _, metric := range postedMetrics {
		if len(metric.Values) != len(metric.Dstypes) || len(metric.Values) != len(metric.Dsnames) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		for i, value := range metric.Values {
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
				Name:    metricName(metric, metric.Dstypes[i], metric.Dsnames[i]),
				Labels:  labels,
				Help:    metricHelp(metric, metric.Dstypes[i], metric.Dsnames[i]),
				Value:   value,
				Type:    metricType(metric.Dstypes[i]),
				Expires: now.Add(time.Duration(metric.Interval) * time.Second * 2),
			}
		}
	}
}

func (c *collectdCollector) processSamples() {
	ticker := time.NewTicker(time.Minute).C
	for {
		select {
		case sample := <-c.ch:
			labelset := collectdSampleLabelset{
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
			c.samples[labelset] = sample
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

// Collect implements prometheus.Collector.
func (c collectdCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- lastPush

	c.mu.Lock()
	samples := make([]*collectdSample, 0, len(c.samples))
	for _, sample := range c.samples {
		samples = append(samples, sample)
	}
	c.mu.Unlock()

	now := time.Now()
	for _, sample := range samples {
		if now.After(sample.Expires) {
			continue
		}
		ch <- prometheus.MustNewConstMetric(
			prometheus.NewDesc(sample.Name, sample.Help, []string{}, sample.Labels),
			sample.Type,
			sample.Value,
		)
	}
}

// Describe implements prometheus.Collector.
func (c collectdCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- lastPush.Desc()
}

func main() {
	flag.Parse()
	http.Handle(*metricsPath, prometheus.Handler())
	c := newCollectdCollector()
	http.HandleFunc(*collectdPostPath, c.collectdPost)
	prometheus.MustRegister(c)
	http.ListenAndServe(*listeningAddress, nil)
}
