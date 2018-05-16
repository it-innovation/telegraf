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

type TestStateSampler struct{}

func (t *TestStateSampler) Sample(sereviceName string) (Sample, error) {
	timestamp := time.Now().UnixNano()
	fmt.Printf("Time stamp %d\n", timestamp)
	s := Sample{name: "active", timestamp: uint64(timestamp)}
	return s, nil
}

func TestAggregateSamples(t *testing.T) {
	var samples = []Sample{
		Sample{name: "active", timestamp: 0},
		Sample{name: "active", timestamp: 2},
	}
	var expectedStates = []State{
		State{name: "active", duration: 2},
	}
	var expectedTotals = map[string]int{"active_dur": 12, "active_count": 1}
	AssertStates(t, samples[0].name, 10, "active", 12, expectedStates, expectedTotals, samples)

	samples = []Sample{
		Sample{name: "active", timestamp: 0},
		Sample{name: "active", timestamp: 2},
		Sample{name: "active", timestamp: 4},
	}
	expectedStates = []State{
		State{name: "active", duration: 4},
	}
	expectedTotals = map[string]int{"active_dur": 14, "active_count": 1}
	AssertStates(t, samples[0].name, 10, "active", 14, expectedStates, expectedTotals, samples)

	samples = []Sample{
		Sample{name: "active", timestamp: 0},
		Sample{name: "failed", timestamp: 2},
	}
	expectedStates = []State{
		State{name: "active", duration: 2},
		State{name: "failed", duration: 0},
	}
	expectedTotals = map[string]int{"active_dur": 2, "active_count": 1, "failed_dur": 0, "failed_count": 1}
	AssertStates(t, samples[0].name, 8, "failed", 0, expectedStates, expectedTotals, samples)

	samples = []Sample{
		Sample{name: "active", timestamp: 0},
		Sample{name: "active", timestamp: 2},
		Sample{name: "inactive", timestamp: 4},
		Sample{name: "active", timestamp: 6},
		Sample{name: "failed", timestamp: 8},
		Sample{name: "inactive", timestamp: 10},
	}
	expectedStates = []State{
		State{name: "active", duration: 4},
		State{name: "inactive", duration: 2},
		State{name: "active", duration: 2},
		State{name: "failed", duration: 2},
		State{name: "inactive", duration: 0},
	}
	expectedTotals = map[string]int{"active_dur": 6, "active_count": 2, "inactive_dur": 2,
		"inactive_count": 2, "failed_dur": 2, "failed_count": 1}
	AssertStates(t, samples[0].name, 2, "inactive", 0, expectedStates, expectedTotals, samples)

	samples = []Sample{
		Sample{name: "active", timestamp: 0},
		Sample{name: "inactive", timestamp: 2},
		Sample{name: "failed", timestamp: 4},
		Sample{name: "active", timestamp: 6},
		Sample{name: "inactive", timestamp: 8},
		Sample{name: "failed", timestamp: 10},
	}
	expectedStates = []State{
		State{name: "active", duration: 2},
		State{name: "inactive", duration: 2},
		State{name: "failed", duration: 2},
		State{name: "active", duration: 2},
		State{name: "inactive", duration: 2},
		State{name: "failed", duration: 0},
	}
	expectedTotals = map[string]int{"active_dur": 4, "active_count": 2, "inactive_dur": 4,
		"inactive_count": 2, "failed_dur": 2, "failed_count": 2}
	AssertStates(t, samples[0].name, 4, "failed", 0, expectedStates, expectedTotals, samples)
}

func AssertStates(t *testing.T, initialState string, initialStateTime uint64, expectedCurrentState string, expectedCrrentStateTime uint64, expectedStates []State, expectedTotals map[string]int, samples []Sample) {
	s := Systemctl{
		SampleRate: 2,
		Aggregator: StateAggregator{
			AggState:             make(map[string]uint64),
			CurrentState:         initialState,
			CurrentStateDuration: initialStateTime,
		},
		Sampler: &TestStateSampler{},
	}

	states, err := s.Aggregator.AggregateSamples(samples)
	if err != nil {
		assert.Error(t, err)
	}

	stateCount := len(states)
	assert.Equal(t, len(expectedStates), stateCount)
	for i := 0; i < stateCount; i++ {
		assert.Equal(t, expectedStates[i].name, states[i].name)
	}

	s.Aggregator.AggregateStates(states, initialStateTime)

	for k := range expectedTotals {
		fmt.Printf("expectedTotals[k] %v, %d \n", k, expectedTotals[k])
		fmt.Printf("s.AggState[k] %v, %d \n", k, s.Aggregator.AggState[k])
		assert.Equal(t, uint64(expectedTotals[k]), s.Aggregator.AggState[k], "Field name %v", k)
	}
}

func TestOnePeriodCollection(t *testing.T) {
	s := Systemctl{
		ServiceName: "nginx",
		SampleRate:  2,
		Aggregator: StateAggregator{
			AggState:             make(map[string]uint64),
			CurrentState:         "unknown",
			CurrentStateDuration: 0,
		},
		Sampler: &TestStateSampler{},

		done:    make(chan bool),
		collect: make(chan bool),
		out:     make(chan []Sample),
	}

	var acc testutil.Accumulator
	s.Start(&acc)
	time.Sleep(10 * time.Second)
	s.Gather(&acc)
	s.Stop()

	fields := map[string]interface{}{
		"current_state":      "active",
		"current_state_time": uint64(8),
		"active_dur":         uint64(8),
		"active_count":       uint64(1),
	}
	tags := map[string]string{"resource": "nginx"}
	AssertContainsTaggedFields(t, &acc, "service_config_state", fields, tags)
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
					assert.Equal(t, expectedDur, actualDur)
				} else {
					assert.Equal(t, fields[k], p.Fields[k])
				}
			}
		}
	}
}
