// Copyright 2026 The Prometheus Authors
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
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/tidwall/gjson"
)

// collectSelfTest runs the two self-test miners on a smartctl JSON document and
// returns "metric{log_type}" -> value.
func collectSelfTest(t *testing.T, doc string) map[string]float64 {
	t.Helper()
	ch := make(chan prometheus.Metric, 32)
	smart := SMARTctl{ch: ch, json: gjson.Parse(doc), device: SMARTDevice{device: "nvme0"}}
	smart.mineDeviceSelfTestLog()
	smart.mineDeviceSelfTestAge()
	close(ch)
	got := map[string]float64{}
	for m := range ch {
		var pb dto.Metric
		if err := m.Write(&pb); err != nil {
			t.Fatal(err)
		}
		name := strings.Split(strings.SplitN(m.Desc().String(), `fqName: "`, 2)[1], `"`)[0]
		var logType string
		for _, l := range pb.GetLabel() {
			if l.GetName() == "self_test_log_type" {
				logType = l.GetValue()
			}
		}
		got[name+"{"+logType+"}"] = pb.GetGauge().GetValue()
	}
	return got
}

func expectMetrics(t *testing.T, got, want map[string]float64) {
	t.Helper()
	for k, v := range want {
		g, ok := got[k]
		if !ok {
			t.Errorf("%s missing, got %v", k, got)
		} else if g != v {
			t.Errorf("%s = %v, want %v", k, g, v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected %s = %v", k, got[k])
		}
	}
}

// Layout from smartmontools 7.5 nvmeprint.cpp, values from server9 nvme0
// (2026-10-31): two short tests, both without error.
func TestSelfTestNVMe(t *testing.T) {
	got := collectSelfTest(t, `{
	  "power_on_time": {"hours": 9400},
	  "nvme_self_test_log": {
	    "nsid": -1,
	    "current_self_test_operation": {"value": 0, "string": "No self-test in progress"},
	    "table": [
	      {"self_test_code": {"value": 1, "string": "Short"}, "self_test_result": {"value": 0, "string": "Completed without error"}, "power_on_hours": 9366},
	      {"self_test_code": {"value": 1, "string": "Short"}, "self_test_result": {"value": 0, "string": "Completed without error"}, "power_on_hours": 4663}
	    ]
	  }
	}`)
	expectMetrics(t, got, map[string]float64{
		"smartctl_device_self_test_age_seconds{short}":    34 * 3600,
		"smartctl_device_self_test_age_seconds{long}":     -1,
		"smartctl_device_self_test_log_count{nvme}":       2,
		"smartctl_device_self_test_log_error_count{nvme}": 0,
	})
}

// A failed extended test does not count as the last long test, and counts as
// an error; aborts (result 1) are neither.
func TestSelfTestNVMeFailedAndAborted(t *testing.T) {
	got := collectSelfTest(t, `{
	  "power_on_time": {"hours": 110},
	  "nvme_self_test_log": {
	    "nsid": -1,
	    "current_self_test_operation": {"value": 0, "string": "No self-test in progress"},
	    "table": [
	      {"self_test_code": {"value": 2, "string": "Extended"}, "self_test_result": {"value": 7, "string": "Completed: failed segments"}, "power_on_hours": 100, "segment": 2},
	      {"self_test_code": {"value": 1, "string": "Short"}, "self_test_result": {"value": 1, "string": "Aborted: Self-test command"}, "power_on_hours": 95},
	      {"self_test_code": {"value": 1, "string": "Short"}, "self_test_result": {"value": 0, "string": "Completed without error"}, "power_on_hours": 90},
	      {"self_test_code": {"value": 2, "string": "Extended"}, "self_test_result": {"value": 0, "string": "Completed without error"}, "power_on_hours": 50}
	    ]
	  }
	}`)
	expectMetrics(t, got, map[string]float64{
		"smartctl_device_self_test_age_seconds{short}":    20 * 3600,
		"smartctl_device_self_test_age_seconds{long}":     60 * 3600,
		"smartctl_device_self_test_log_count{nvme}":       4,
		"smartctl_device_self_test_log_error_count{nvme}": 1,
	})
}

// An NVMe drive that never ran a test: smartctl prints the log without a table
// (server8 nvme1, 2026-10-31). Ages stay -1, no log counts.
func TestSelfTestNVMeNeverTested(t *testing.T) {
	got := collectSelfTest(t, `{
	  "power_on_time": {"hours": 27000},
	  "nvme_self_test_log": {"nsid": -1, "current_self_test_operation": {"value": 0, "string": "No self-test in progress"}}
	}`)
	expectMetrics(t, got, map[string]float64{
		"smartctl_device_self_test_age_seconds{short}": -1,
		"smartctl_device_self_test_age_seconds{long}":  -1,
	})
}

// The ATA path is unchanged: newest successful entry per type, 16-bit
// lifetime_hours unwrapped (server1 sdb shape, 66,013 h).
func TestSelfTestATAUnchanged(t *testing.T) {
	got := collectSelfTest(t, `{
	  "power_on_time": {"hours": 66013},
	  "ata_smart_self_test_log": {"standard": {"revision": 1, "count": 2, "error_count_total": 0, "error_count_outdated": 0,
	    "table": [
	      {"type": {"value": 1, "string": "Short offline"}, "status": {"value": 0, "string": "Completed without error", "passed": true}, "lifetime_hours": 65857},
	      {"type": {"value": 2, "string": "Extended offline"}, "status": {"value": 0, "string": "Completed without error", "passed": true}, "lifetime_hours": 65628}
	    ]}}
	}`)
	expectMetrics(t, got, map[string]float64{
		"smartctl_device_self_test_age_seconds{short}":        156 * 3600,
		"smartctl_device_self_test_age_seconds{long}":         385 * 3600,
		"smartctl_device_self_test_log_count{standard}":       2,
		"smartctl_device_self_test_log_error_count{standard}": 0,
	})
}

// Upstream's Describe lists every descriptor explicitly (#329); a metric the
// collector sends without being listed fails the scrape ("unregistered
// descriptor"). The self-test metrics must all be listed.
func TestSelfTestDescribed(t *testing.T) {
	ch := make(chan *prometheus.Desc, 128)
	(&SMARTctlManagerCollector{}).Describe(ch)
	close(ch)
	described := map[*prometheus.Desc]bool{}
	for d := range ch {
		described[d] = true
	}
	for name, d := range map[string]*prometheus.Desc{
		"self_test_age_seconds":     metricDeviceSelfTestAge,
		"self_test_log_count":       metricDeviceSelfTestLogCount,
		"self_test_log_error_count": metricDeviceSelfTestLogErrorCount,
	} {
		if !described[d] {
			t.Errorf("smartctl_device_%s is not in Describe", name)
		}
	}
}
