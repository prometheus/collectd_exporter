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
	"reflect"
	"sync"
	"testing"
	"time"

	"collectd.org/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/common/promslog"
)

func TestNewName(t *testing.T) {
	cases := []struct {
		vl    api.ValueList
		index int
		want  string
	}{
		{api.ValueList{
			Identifier: api.Identifier{
				Plugin: "cpu",
				Type:   "cpu",
			},
			DSNames: []string{"value"},
			Values:  []api.Value{api.Derive(0)},
		}, 0, "collectd_cpu_total"},
		{api.ValueList{
			Identifier: api.Identifier{
				Plugin: "dns",
				Type:   "dns_qtype",
			},
			DSNames: []string{"value"},
			Values:  []api.Value{api.Derive(0)},
		}, 0, "collectd_dns_dns_qtype_total"},
		{api.ValueList{
			Identifier: api.Identifier{
				Plugin: "df",
				Type:   "df",
			},
			DSNames: []string{"used", "free"},
			Values:  []api.Value{api.Gauge(0), api.Gauge(1)},
		}, 0, "collectd_df_used"},
		{api.ValueList{
			Identifier: api.Identifier{
				Plugin: "df",
				Type:   "df",
			},
			DSNames: []string{"used", "free"},
			Values:  []api.Value{api.Gauge(0), api.Gauge(1)},
		}, 1, "collectd_df_free"},
		{api.ValueList{
			Identifier: api.Identifier{
				Plugin: "cpu",
				Type:   "percent",
			},
			DSNames: []string{"value"},
			Values:  []api.Value{api.Gauge(0)},
		}, 0, "collectd_cpu_percent"},
		{api.ValueList{
			Identifier: api.Identifier{
				Plugin: "interface",
				Type:   "if_octets",
			},
			DSNames: []string{"rx", "tx"},
			Values:  []api.Value{api.Counter(0), api.Counter(1)},
		}, 0, "collectd_interface_if_octets_rx_total"},
		{api.ValueList{
			Identifier: api.Identifier{
				Plugin: "interface",
				Type:   "if_octets",
			},
			DSNames: []string{"rx", "tx"},
			Values:  []api.Value{api.Counter(0), api.Counter(1)},
		}, 1, "collectd_interface_if_octets_tx_total"},
		{api.ValueList{
			Identifier: api.Identifier{
				Plugin: "docker",
				Type:   "cpu.percent",
			},
			DSNames: []string{"value"},
			Values:  []api.Value{api.Gauge(0)},
		}, 0, "collectd_docker_cpu_percent"},
	}

	for _, c := range cases {
		got := newName(c.vl, c.index)
		if got != c.want {
			t.Errorf("newName(%v): got %q, want %q", c.vl, got, c.want)
		}
	}
}

func TestNewLabels(t *testing.T) {
	cases := []struct {
		vl   api.ValueList
		want prometheus.Labels
	}{
		{api.ValueList{
			Identifier: api.Identifier{
				Host:           "example.com",
				Plugin:         "cpu",
				PluginInstance: "0",
				Type:           "cpu",
				TypeInstance:   "user",
			},
		}, prometheus.Labels{
			"cpu":      "0",
			"type":     "user",
			"instance": "example.com",
		}},
		{api.ValueList{
			Identifier: api.Identifier{
				Host:         "example.com",
				Plugin:       "df",
				Type:         "df_complex",
				TypeInstance: "used",
			},
		}, prometheus.Labels{
			"df":       "used",
			"instance": "example.com",
		}},
		{api.ValueList{
			Identifier: api.Identifier{
				Host:   "example.com",
				Plugin: "load",
				Type:   "load",
			},
		}, prometheus.Labels{
			"instance": "example.com",
		}},
	}

	for _, c := range cases {
		got := newLabels(c.vl)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("newLabels(%v): got %v, want %v", c.vl, got, c.want)
		}
	}
}

// TestCollectMixedDataSourceTypes checks that a counter reported as COUNTER by
// one host and as DERIVE by another (e.g. if_errors from collectd 4.x and 5.x
// types.db) is exported as one consistent metric family. See #100.
func TestCollectMixedDataSourceTypes(t *testing.T) {
	now := time.Now()
	newVL := func(host string, v api.Value) api.ValueList {
		return api.ValueList{
			Identifier: api.Identifier{
				Host:           host,
				Plugin:         "interface",
				PluginInstance: "eth0",
				Type:           "if_errors",
			},
			Time:     now,
			Interval: 10 * time.Second,
			Values:   []api.Value{v, v},
			DSNames:  []string{"rx", "tx"},
		}
	}

	c := &collectdCollector{
		valueLists: map[string]api.ValueList{},
		mu:         &sync.Mutex{},
		logger:     promslog.NewNopLogger(),
	}
	for _, vl := range []api.ValueList{
		newVL("collectd4.example.com", api.Counter(1)),
		newVL("collectd5.example.com", api.Derive(2)),
	} {
		c.valueLists[vl.String()] = vl
	}

	reg := prometheus.NewRegistry()
	reg.MustRegister(c)
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() returned an error: %v", err)
	}

	got := map[string]int{}
	for _, mf := range mfs {
		got[mf.GetName()] = len(mf.GetMetric())
	}
	for _, name := range []string{
		"collectd_interface_if_errors_rx_total",
		"collectd_interface_if_errors_tx_total",
	} {
		if got[name] != 2 {
			t.Errorf("%s: got %d series, want 2", name, got[name])
		}
	}
}
