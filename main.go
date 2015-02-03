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
	Values          []float64
	Dstypes         []string
	Dsnames         []string
	Time            float64
	Interval        float64
	Host            string
	Plugin          string
	Plugin_instance string
	Type            string
	Type_instance   string
}

var (
	addr     = flag.String("listen-address", ":1234", "The address to listen on for HTTP requests.")
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
			if metric.Plugin_instance != "" {
				labels[metric.Plugin] = metric.Plugin_instance
			}
			if metric.Type_instance != "" {
				if metric.Plugin_instance == "" {
					labels[metric.Plugin] = metric.Type_instance
				} else {
					labels["type"] = metric.Type_instance
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
