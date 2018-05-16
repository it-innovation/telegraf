package systemctl

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs"
)

// Systemctl is an telegraf serive input plugin for systemctl service state
type Systemctl struct {
	ServiceName string
	SampleRate  int

	Aggregator StateAggregator
	Sampler    StateSampler

	mux     sync.Mutex
	running bool
	done    chan bool
	collect chan bool
	out     chan []Sample
}

const sampleConfig = `
	sample_rate = 2
	# service_name = "nginx"
`

// SampleConfig returns the sample configuration for the input plugin
func (s *Systemctl) SampleConfig() string {
	return sampleConfig
}

// Description returns a short description of the input pluging
func (s *Systemctl) Description() string {
	return "Systemctl state monitor"
}

// Gather collects the samples and adds the AggState to the telegraf accumulator
func (s *Systemctl) Gather(acc telegraf.Accumulator) error {
	s.mux.Lock()
	defer s.mux.Unlock()

	// notify sampler of collection
	s.collect <- true
	// read samples
	samples := <-s.out
	// aggregate samples into states
	states, err := s.Aggregator.AggregateSamples(samples)
	if err != nil {
		return err
	}
	// add states to aggregation
	s.Aggregator.AggregateStates(states, s.Aggregator.CurrentStateDuration)
	// create fields
	fields := map[string]interface{}{
		"current_state_time": s.Aggregator.CurrentStateDuration,
		"current_state":      s.Aggregator.CurrentState,
	}
	for k := range s.Aggregator.AggState {
		fields[k] = s.Aggregator.AggState[k]
	}
	// create tag
	tags := map[string]string{"resource": s.ServiceName}
	acc.AddFields("service_config_state", fields, tags)
	return nil
}

// Start starts the sampling of systemctl state for the configured services
func (s *Systemctl) Start(acc telegraf.Accumulator) error {
	s.mux.Lock()
	defer s.mux.Unlock()
	if s.running {
		return nil
	}

	go s.CollectSamples(s.done, s.collect, s.out)
	s.running = true

	return nil
}

// CollectSamples samples system control state
func (s *Systemctl) CollectSamples(done chan bool, collect chan bool, out chan []Sample) {
	samples := make([]Sample, 0)
	for {
		select {
		default:
			sample, err := s.Sampler.Sample(s.ServiceName)
			if err != nil {
				fmt.Printf("Warning error reading sample %s\n", err)
			} else {
				samples = append(samples, sample)
				time.Sleep(time.Duration(s.SampleRate) * time.Second)
			}
		case <-collect:
			out <- samples
			lastSample := samples[len(samples)-1]
			samples = make([]Sample, 1)
			samples[0] = lastSample
		case <-done:
			return
		}
	}
}

// Stop stops the sampling of systemctrl state for the configured services
func (s *Systemctl) Stop() {
	s.mux.Lock()
	defer s.mux.Unlock()
	if !s.running {
		return
	}

	s.done <- false
	s.running = false
}

// Sampler is a sampler for systemctl states
type Sampler struct{}

// ActiveStateKey systemctl active state key
const ActiveStateKey string = "ActiveState"

// SubStateKey systemctl substate key
const SubStateKey string = "SubState"

// LoadStateKey systemctl load state key
const LoadStateKey string = "LoadState"

// Sample implements state sampling
func (s *Sampler) Sample(serviceName string) (Sample, error) {
	var sample Sample

	out, err := exec.Command("systemctl", "--no-pager", "show", serviceName).Output()
	if err != nil {
		return sample, errors.New("error occured executing sample command")
	}

	var loadState string
	var activeState string
	var subState string
	lines := strings.Split(string(out[:]), "\n")
	for _, line := range lines {
		parts := strings.Split(line, "=")
		if parts[0] == LoadStateKey {
			loadState = parts[1]
		} else if parts[0] == ActiveStateKey {
			activeState = parts[1]
		} else if parts[0] == SubStateKey {
			subState = parts[1]
		}
	}

	// create state name
	var b bytes.Buffer
	b.WriteString(loadState)
	b.WriteString(".")
	b.WriteString(activeState)
	b.WriteString(".")
	b.WriteString(subState)

	// create sample
	timestamp := time.Now().UnixNano()
	sample = Sample{name: b.String(), timestamp: uint64(timestamp)}

	return sample, nil
}

func init() {
	inputs.Add("systemctl", func() telegraf.Input {
		return &Systemctl{
			ServiceName: "nginx",
			SampleRate:  2,

			Aggregator: StateAggregator{
				AggState:             make(map[string]uint64),
				CurrentState:         "unknown",
				CurrentStateDuration: 0,
			},
			Sampler: &Sampler{},

			done:    make(chan bool),
			collect: make(chan bool),
			out:     make(chan []Sample),
		}
	})
}
