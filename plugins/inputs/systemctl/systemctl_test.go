package systemctl

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/influxdata/telegraf/testutil"
	"github.com/stretchr/testify/assert"
)

type MockSampler struct{}

func (t *MockSampler) Sample(serviceName string) (Sample, error) {
	timestamp := time.Now().UnixNano()
	//fmt.Printf("Time stamp %d\n", timestamp)
	s := Sample{name: "active", timestamp: uint64(timestamp)}
	return s, nil
}

func TestOnePeriodOneServiceCollection(t *testing.T) {
	services := []string{"nginx"}

	s := Systemctl{
		Services:   services,
		SampleRate: 2,
		Sampler:    &MockSampler{},
	}

	var acc testutil.Accumulator
	s.Start(&acc)
	time.Sleep(5 * time.Second)
	s.Gather(&acc)
	s.Stop()

	results := make([]map[string]interface{}, 2)

	results[0] = map[string]interface{}{
		"current_state":      "active",
		"current_state_time": uint64(4),
		"active_dur":         uint64(4),
		"active_count":       uint64(1),
	}

	for i, service := range services {
		fmt.Printf("Checking : %v\n", service)
		tags := map[string]string{"resource": service}
		AssertContainsTaggedFields(t, &acc, "service_config_state", results[i], tags)
	}
}

func TestOnePeriodCollection(t *testing.T) {
	services := []string{"nginx", "influxdb"}

	s := Systemctl{
		Services:   services,
		SampleRate: 2,
		Sampler:    &MockSampler{},
	}

	var acc testutil.Accumulator
	s.Start(&acc)
	time.Sleep(5 * time.Second)
	s.Gather(&acc)
	s.Stop()

	results := make([]map[string]interface{}, 2)

	results[0] = map[string]interface{}{
		"current_state":      "active",
		"current_state_time": uint64(4),
		"active_dur":         uint64(4),
		"active_count":       uint64(1),
	}

	results[1] = map[string]interface{}{
		"current_state":      "active",
		"current_state_time": uint64(6),
		"active_dur":         uint64(6),
		"active_count":       uint64(1),
	}

	for i, service := range services {
		fmt.Printf("Checking : %v\n", service)
		tags := map[string]string{"resource": service}
		AssertContainsTaggedFields(t, &acc, "service_config_state", results[i], tags)
	}
}

func AssertContainsTaggedFields(
	t *testing.T,
	a *testutil.Accumulator,
	measurement string,
	fields map[string]interface{},
	tags map[string]string,
) {
	a.Lock()
	defer a.Unlock()
	for _, p := range a.Metrics {
		if !reflect.DeepEqual(tags, p.Tags) {
			continue
		}

		if p.Measurement == measurement {
			for k := range fields {
				if strings.HasSuffix(k, "_dur") || k == "current_state_time" {

					expectedDur := fields[k].(uint64)
					actualDur := p.Fields[k].(uint64) / 1000000000
					//	fmt.Printf("expectedDur : %d\n", expectedDur)
					//	fmt.Printf("actualDur : %d\n", actualDur)
					assert.Equal(t, expectedDur, actualDur)
				} else {
					assert.Equal(t, fields[k], p.Fields[k])
				}
			}
		}
	}
}
