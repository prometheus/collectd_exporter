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
	"testing"

	"collectd.org/api"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/prometheus/client_golang/prometheus"
	"strings"
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
		md   metadata
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
		}, metadata{
			map[string]string{
				"foo":           "bar",
				ETagApplication: "Appy",
				ETagEnvironment: "Envy",
			},
			"fakeHost",
			"10.0.1.1",
		}, prometheus.Labels{
			"cpu":      "0",
			"type":     "user",
			"instance": "example.com",
			"host":     "fakeHost",
			strings.ToLower(ETagApplication): "Appy",
			strings.ToLower(ETagEnvironment): "Envy",
		},
		},

		{api.ValueList{
			Identifier: api.Identifier{
				Host:         "example.com",
				Plugin:       "df",
				Type:         "df_complex",
				TypeInstance: "used",
			},
		}, metadata{
			map[string]string{
				"foo":           "bar",
				ETagApplication: "Appy",
				ETagEnvironment: "Envy",
				ETagStack:       "Stacky",
				ETagRole:        "Roley",
			},
			"i-a1b2c3",
			"10.0.1.1",
		}, prometheus.Labels{
			"df":       "used",
			"instance": "example.com",
			"host":     "i-a1b2c3",
			strings.ToLower(ETagApplication):                                  "Appy",
			strings.ToLower(ETagEnvironment):                                  "Envy",
			strings.ToLower(strings.Join([]string{ETagStack, ETagRole}, "_")): "Stacky_Roley",
		},
		},

		{api.ValueList{
			Identifier: api.Identifier{
				Host:   "example.com",
				Plugin: "load",
				Type:   "load",
			},
		}, metadata{
			map[string]string{
				ETagEnvironment: Untagged,
				ETagApplication: Untagged,
				ETagStack:       Untagged,
				ETagRole:        "Roley",
				ETagName:        "Namey",
			},
			"i-98765",
			"10.0.2.2",
		}, prometheus.Labels{
			"instance": "example.com",
			"host":     "i-98765",
			strings.ToLower(ETagApplication):                                  Untagged,
			strings.ToLower(ETagEnvironment):                                  Untagged,
			strings.ToLower(strings.Join([]string{ETagStack, ETagRole}, "_")): strings.Join([]string{Untagged, "Roley"}, "_"),
			strings.ToLower(ETagName):                                         "Namey",
		}},
	}

	for _, c := range cases {
		got := newLabels(c.vl, c.md)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("newLabels(%v): got %v, want %v", c.vl, got, c.want)
		}
	}
}

// TODO: Move this to a "common" package
// Utility function to get pointer to a string literal as Go does not support pointer generation to a literal,
// e.g. sPtr := &"hello" (not supported)
func stringAddr(s string) *string {
	return &s
}

func TestBackfillTags(t *testing.T) {
	//mockCollector := newCollectdCollector()

	cases := []struct {
		mockCollector *collectdCollector
		ec2TagsOutput *ec2.DescribeTagsOutput
		want          map[string]string
	}{
		// Happy path
		{
			newCollectdCollector(),
			&ec2.DescribeTagsOutput{
				NextToken: stringAddr("123456"),
				Tags: []*ec2.TagDescription{
					&ec2.TagDescription{
						Key:   stringAddr(ETagApplication),
						Value: stringAddr("Appy"),
					},
					&ec2.TagDescription{
						Key:   stringAddr(ETagEnvironment),
						Value: stringAddr("Envy"),
					},
					&ec2.TagDescription{
						Key:   stringAddr(ETagStack),
						Value: stringAddr("Stacky"),
					},
					&ec2.TagDescription{
						Key:   stringAddr(ETagRole),
						Value: stringAddr("Roley"),
					},
					&ec2.TagDescription{
						Key:   stringAddr(ETagName),
						Value: stringAddr("Namey"),
					},
				},
			},
			map[string]string{
				ETagEnvironment: "Envy",
				ETagApplication: "Appy",
				ETagStack:       "Stacky",
				ETagRole:        "Roley",
				ETagName:        "Namey",
			},
		},
		// Missing tags
		{
			newCollectdCollector(),
			&ec2.DescribeTagsOutput{
				NextToken: stringAddr("123456"),
				Tags: []*ec2.TagDescription{
					&ec2.TagDescription{
						Key:   stringAddr(ETagApplication),
						Value: stringAddr("Appy"),
					},
					&ec2.TagDescription{
						Key:   stringAddr(ETagEnvironment),
						Value: stringAddr("Envy"),
					},
				},
			},
			map[string]string{
				ETagEnvironment: "Envy",
				ETagApplication: "Appy",
				ETagStack:       Untagged,
				ETagRole:        Untagged,
				ETagName:        Untagged,
			},
		},
		// Missing tag where one of the required tag values is an empty string
		{
			newCollectdCollector(),
			&ec2.DescribeTagsOutput{
				NextToken: stringAddr("67890"),
				Tags: []*ec2.TagDescription{
					&ec2.TagDescription{
						Key:   stringAddr(ETagEnvironment),
						Value: stringAddr("Envy"),
					},
					&ec2.TagDescription{
						Key:   stringAddr(ETagApplication),
						Value: stringAddr("Appy"),
					},
					&ec2.TagDescription{
						Key:   stringAddr(ETagStack),
						Value: stringAddr(""),
					},
				},
			},
			map[string]string{
				ETagEnvironment: "Envy",
				ETagApplication: "Appy",
				ETagStack:       Untagged,
				ETagRole:        Untagged,
				ETagName:        Untagged,
			},
		},
		// Missing tags and tags that are not required
		{
			newCollectdCollector(),
			&ec2.DescribeTagsOutput{
				NextToken: stringAddr("67890"),
				Tags: []*ec2.TagDescription{
					&ec2.TagDescription{
						Key:   stringAddr(ETagEnvironment),
						Value: stringAddr("Envy"),
					},
					&ec2.TagDescription{
						Key:   stringAddr(ETagApplication),
						Value: stringAddr("Appy"),
					},
					&ec2.TagDescription{
						Key:   stringAddr(ETagStack),
						Value: stringAddr(""),
					},
					// Not a required tag. Will be filtered out
					&ec2.TagDescription{
						Key:   stringAddr("foo"),
						Value: stringAddr("bar"),
					},
				},
			},
			map[string]string{
				ETagEnvironment: "Envy",
				ETagApplication: "Appy",
				ETagStack:       Untagged,
				ETagRole:        Untagged,
				ETagName:        Untagged,
			},
		},
	}
	for _, c := range cases {
		backfillTags(c.mockCollector, c.ec2TagsOutput)
		if !reflect.DeepEqual(c.mockCollector.md.tags, c.want) {
			t.Errorf("newLabels: got %v, want %v", c.mockCollector.md.tags, c.want)
		}
	}
}
