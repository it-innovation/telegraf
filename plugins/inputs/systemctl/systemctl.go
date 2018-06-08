package systemctl

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/internal"
	"github.com/influxdata/telegraf/plugins/inputs"

	log "github.com/Sirupsen/logrus"
)

// Systemctl is an telegraf serive input plugin for systemctl service state
type Systemctl struct {
	// plugin configuration, list of systemctl services being monitoring
	Services []string
	// plugin configuration, sample rate for polling systemctl
	SampleRate int
	// plugin configuration, set the logging level for the plugin
	LogLevel string

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
	
	## Logging level for this plugin according to logrus log levels
	## "debug", "info", "warning", "error", "fatal", "panic"
	## log_level = "info"
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

	// for each systemctl service being monitored
	for _, aggregator := range s.Aggregators {
		// aggregate the data from the set of samples
		log.WithFields(log.Fields{
			"InputPlugin":  "systemctl",
			"ResourceName": aggregator.ResourceName,
		}).Debug("Aggregating")
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
		// create tags
		tags := map[string]string{"resource": aggregator.ResourceName}
		acc.AddFields("service_config_state", fields, tags)
		log.WithFields(log.Fields{
			"InputPlugin":  "systemctl",
			"ResourceName": aggregator.ResourceName,
		}).Debug("Added fields")
	}
	return nil
}

// Start starts the sampling of systemctl state for the configured services
func (s *Systemctl) Start(acc telegraf.Accumulator) error {
	// lock the function
	s.mux.Lock()
	// release the lock at the end of the function
	defer s.mux.Unlock()
	// check that the sampler has been initiatised
	if s.Sampler == nil {
		return errors.New("Systemctl.Sampler has not been set")
	}
	// check the sampler is not already running
	if s.running {
		return nil
	}

	SetLogLevel(s.LogLevel)
	log.WithFields(log.Fields{
		"InputPlugin": "systemctl",
	}).Debug("Starting")

	// check the sample has not already initalised the aggregators
	if s.Aggregators == nil {
		// create an aggregator for each service defined within the configuration
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
			// start collecting samples for each aggregator in a separate go routine
			// providing the service name and the sampler used to collect data
			log.WithFields(log.Fields{
				"InputPlugin":  "systemctl",
				"ResourceName": service,
			}).Debug("Starting CollectSamples")
			go s.Aggregators[i].StateCollector.CollectSamples(service, s.Sampler)
		}
	}

	// set the state that the input plugin has started
	s.running = true

	log.WithFields(log.Fields{
		"InputPlugin": "systemctl",
	}).Debug("Started")

	return nil
}

// Stop stops the sampling of systemctrl state for the configured services
func (s *Systemctl) Stop() {
	// lock the function
	s.mux.Lock()
	// release the lock at the end of the function
	defer s.mux.Unlock()

	// return if not running
	if !s.running {
		return
	}

	log.WithFields(log.Fields{
		"InputPlugin": "systemctl",
	}).Debug("Stopping")

	// create a wait group the size of the number of aggregators
	var wg sync.WaitGroup
	wg.Add(len(s.Aggregators))
	// stop each aggregator by sending a false message to the done channel
	// and when finished indicate that it's completed by setting WG-Done
	for _, aggregator := range s.Aggregators {
		go func(aggregator StateAggregator) {
			defer wg.Done()
			aggregator.StateCollector.Done <- false
		}(aggregator)
	}
	// wait for all go routines to finish in the wait group
	wg.Wait()

	// set the state to not running
	s.running = false

	log.WithFields(log.Fields{
		"InputPlugin": "systemctl",
	}).Debug("Stopped")
}

// ServiceSampler is a sampler for systemctl states
type ServiceSampler struct{}

// ActiveStateKey systemctl active state key
const ActiveStateKey string = "ActiveState"

// SubStateKey systemctl substate key
const SubStateKey string = "SubState"

// LoadStateKey systemctl load state key
const LoadStateKey string = "LoadState"

// Sample implements state sampling for a specific systemctl service
func (s *ServiceSampler) Sample(serviceName string) (Sample, error) {
	var sample Sample

	// execute the os command to get the status of the systemctl service
	c := exec.Command("systemctl", "--no-pager", "show", serviceName)
	out, err := internal.CombinedOutputTimeout(c,
		time.Second*time.Duration(2))

	if err != nil {
		sampleCmd := "systemctl --no-pager show " + serviceName
		log.WithFields(log.Fields{
			"InputPlugin":   "systemctl",
			"SampleCommand": sampleCmd,
		}).Error("Error executing the sample command")

		return sample, errors.New("error occured executing sample command")
	}

	// parse the key value pairs in the command output to find the load, active and sub state keys
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

	// create the current state as the concatination of the three states
	// load.active.sub
	var b bytes.Buffer
	b.WriteString(loadState)
	b.WriteString(".")
	b.WriteString(activeState)
	b.WriteString(".")
	b.WriteString(subState)

	// create sample
	timestamp := time.Now().UnixNano()
	sample = Sample{name: b.String(), timestamp: uint64(timestamp)}

	log.WithFields(log.Fields{
		"InputPlugin":  "systemctl",
		"Sample state": b.String(),
		"Sample time":  uint64(timestamp),
	}).Debug("Collected Sample")

	return sample, nil
}

// initialise the plugin
func init() {
	inputs.Add("systemctl", func() telegraf.Input {
		return &Systemctl{
			Services:   []string{"nginx"},
			SampleRate: 2,
			LogLevel:   "info",
			Sampler:    &ServiceSampler{},
		}
	})
}

// SetLogLevel converts to plugin config string to a log level
func SetLogLevel(level string) {
	switch level {
	case "debug":
		log.SetLevel(log.DebugLevel)
	case "info":
		log.SetLevel(log.InfoLevel)
	case "warning":
		log.SetLevel(log.WarnLevel)
	case "error":
		log.SetLevel(log.ErrorLevel)
	case "fatal":
		log.SetLevel(log.FatalLevel)
	case "panic":
		log.SetLevel(log.PanicLevel)
	default:
		log.SetLevel(log.InfoLevel)
		msg := "Unknown level, " + level
		log.WithFields(log.Fields{
			"InputPlugin": "systemctl",
		}).Error(msg)
	}
}
