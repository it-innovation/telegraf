package systemctl

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/plugins/inputs"
)

// Systemctl is an telegraf serive input plugin for systemctl service state
type Systemctl struct {
	Services   []string
	SampleRate int

	Aggregators []StateAggregator
	Sampler     StateSampler

	mux     sync.Mutex
	running bool
}

const sampleConfig = `
	## Service array
	services = [
	  "ssh.service",
	  "cron.service",
	]

	## Sample rate for sampling state of service.
	## Must be greter that the collection_interval/2 
	sample_rate = 2
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

	for _, aggregator := range s.Aggregators {
		err := aggregator.Aggregate()
		if err != nil {
			return err
		}
		// create fields
		fields := map[string]interface{}{
			"current_state_time": aggregator.CurrentStateDuration,
			"current_state":      aggregator.CurrentState,
		}
		for k := range aggregator.AggState {
			fields[k] = aggregator.AggState[k]
		}
		// create tag
		tags := map[string]string{"resource": aggregator.ResourceName}
		acc.AddFields("service_config_state", fields, tags)
	}
	return nil
}

// Start starts the sampling of systemctl state for the configured services
func (s *Systemctl) Start(acc telegraf.Accumulator) error {
	s.mux.Lock()
	defer s.mux.Unlock()
	if s.Sampler == nil {
		return errors.New("Systemctl.Sampler has not been set")
	}

	if s.running {
		return nil
	}

	if s.Aggregators == nil {
		serviceCount := len(s.Services)
		s.Aggregators = make([]StateAggregator, serviceCount)
		for i, service := range s.Services {
			s.Aggregators[i] = StateAggregator{
				ResourceName:         service,
				AggState:             make(map[string]uint64),
				CurrentState:         "unknown",
				CurrentStateDuration: 0,
				StateCollector: Collector{
					SampleRate:    s.SampleRate,
					Done:          make(chan bool),
					Collect:       make(chan bool),
					SampleResults: make(chan []Sample),
				},
			}

			go s.Aggregators[i].StateCollector.CollectSamples(service, s.Sampler)
		}
	}
	s.running = true

	return nil
}

// Stop stops the sampling of systemctrl state for the configured services
func (s *Systemctl) Stop() {
	s.mux.Lock()
	defer s.mux.Unlock()
	if !s.running {
		return
	}

	var wg sync.WaitGroup
	wg.Add(len(s.Aggregators))
	for _, aggregator := range s.Aggregators {
		go func(aggregator StateAggregator) {
			defer wg.Done()
			aggregator.StateCollector.Done <- false
		}(aggregator)
	}
	wg.Wait()
	s.running = false
}

// ServiceSampler is a sampler for systemctl states
type ServiceSampler struct{}

// ActiveStateKey systemctl active state key
const ActiveStateKey string = "ActiveState"

// SubStateKey systemctl substate key
const SubStateKey string = "SubState"

// LoadStateKey systemctl load state key
const LoadStateKey string = "LoadState"

// Sample implements state sampling
func (s *ServiceSampler) Sample(serviceName string) (Sample, error) {
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
			Services:   []string{"nginx"},
			SampleRate: 2,
			Sampler:    &ServiceSampler{},
		}
	})
}
