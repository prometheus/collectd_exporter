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
	addr = flag.String("listen-address", ":1234", "The address to listen on for HTTP requests.")
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
	json.NewDecoder(r.Body).Decode(&postedMetrics)
	now := time.Now()
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
				Expires: now.Add(time.Duration(metric.Interval) * time.Second * 2),
			}
		}
	}
}

type collectdSample struct {
	Name    string
	Labels  map[string]string
	Help    string
	Value   float64
	Expires time.Time
}

type collectdSampleLabelset struct {
	Name           string
	Instance       string
	Type           string
	Plugin         string
	PluginInstance string
}

func (c *CollectdCollector) processSamples() {
	for {
		sample := <-c.ch
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
	}
}

// Implements Collector.
func (c CollectdCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	samples := c.samples
	c.mu.Unlock()
	now := time.Now()
	for _, sample := range samples {
    if now.After(sample.Expires) {
      continue
    }
		gauge := prometheus.NewGauge(
			prometheus.GaugeOpts{
				Name:        sample.Name,
				Help:        sample.Help,
				ConstLabels: sample.Labels})
		gauge.Set(sample.Value)
		ch <- gauge
	}
}

// Implements Collector.
func (c CollectdCollector) Describe(ch chan<- *prometheus.Desc) {
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "collectd_dummy",
		Help: "dummy",
	})
	ch <- gauge.Desc()
}

func main() {
	flag.Parse()
	http.Handle("/metrics", prometheus.Handler())
	c := newCollectdCollector()
	http.HandleFunc("/collectd-post", c.collectdPost)
	prometheus.MustRegister(c)
	http.ListenAndServe(*addr, nil)
}
