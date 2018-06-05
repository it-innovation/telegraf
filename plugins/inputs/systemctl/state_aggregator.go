package systemctl

import (
	"bytes"
	"errors"
	"time"
)

// StateAggregator is an utility to calc state statistics
type StateAggregator struct {
	ResourceName         string
	AggState             map[string]uint64
	CurrentState         string
	CurrentStateDuration uint64
	StateCollector       Collector
}

// State is a systemctl service state and it's duration
type State struct {
	name     string
	duration uint64
}

// Sample is a single sample of systemctl service state with timestamp
type Sample struct {
	name      string
	timestamp uint64
}

// StateSampler is an interface to collect state samples
type StateSampler interface {
	Sample(serviceName string) (Sample, error)
}

// DurFieldSuffix is the suffix appended to create unique field names for state duration
const DurFieldSuffix string = "_dur"

// CountFieldSuffix is the suffix appended to create unique field names for state count
const CountFieldSuffix string = "_count"

// Collector is a function to continously collect samples
type Collector struct {
	SampleRate    int
	Done          chan bool
	Collect       chan bool
	SampleResults chan []Sample
}

// CollectSamples samples system control state
func (c *Collector) CollectSamples(resourceName string, sampler StateSampler) {
	samples := make([]Sample, 0)
	for {
		select {
		default:
			sample, err := sampler.Sample(resourceName)
			if err != nil {
				//fmt.Printf("Warning error reading sample %s\n", err)
			} else {
				samples = append(samples, sample)
				time.Sleep(time.Duration(c.SampleRate) * time.Second)
			}
		case <-c.Collect:
			c.SampleResults <- samples
			lastSample := samples[len(samples)-1]
			samples = make([]Sample, 1)
			samples[0] = lastSample
		case <-c.Done:
			return
		}
	}
}

// Aggregate aggregates the current set of samples with the previous samples
func (a *StateAggregator) Aggregate() error {
	// notify sampler of collection
	a.StateCollector.Collect <- true
	// read samples
	samples := <-a.StateCollector.SampleResults
	// aggregate samples into states
	states, err := a.AggregateSamples(samples)
	if err != nil {
		return err
	}
	// add states to aggregation
	a.AggregateStates(states, a.CurrentStateDuration)

	return nil
}

// AggregateSamples creates states and their duration from a set of samples
func (a *StateAggregator) AggregateSamples(samples []Sample) ([]State, error) {
	sampleCount := len(samples)
	// error if no samples to aggregate
	if sampleCount < 2 {
		return nil, errors.New("2 or more samples needed for aggregation")
	}

	states := make([]State, 0)
	var stateTime uint64
	var stateStartTime uint64
	// for the 1st sample we set the current state and state_start_time
	currentState := samples[0].name
	stateStartTime = samples[0].timestamp
	lastIndex := sampleCount - 1
	for i := 1; i < sampleCount; i++ {
		if currentState != samples[i].name {
			// calc duration in current state
			stateTime = samples[i].timestamp - stateStartTime
			states = append(states, State{name: currentState, duration: stateTime})
			// set the new start time and current state
			stateStartTime = stateStartTime + stateTime
			currentState = samples[i].name
		}
		// if the last sample
		if i == lastIndex {
			// if transition in the last sample, add last state with zero duration
			if currentState != samples[i].name {
				// add next state with zero transition
				states = append(states, State{name: samples[i].name, duration: 0})
			} else {
				// calc duration in current state
				stateTime = samples[i].timestamp - stateStartTime
				states = append(states, State{name: currentState, duration: stateTime})
			}
		}
	}
	return states, nil
}

// GetKeyName creates concatinates two strings to create a key name for the AggState map
func (a *StateAggregator) GetKeyName(currentState string, suffix string) string {
	var b bytes.Buffer

	b.WriteString(currentState)
	b.WriteString(suffix)

	return b.String()
}

// AggregateStates creates AggState from a set of states
func (a *StateAggregator) AggregateStates(states []State, currentStateDuration uint64) {
	var stateDurKey string
	var stateCountKey string
	var containsState bool

	// set initial state to the 1st sample
	initialState := states[0].name
	// set the current state as the last state sampled
	stateCount := len(states)
	currentState := states[stateCount-1].name
	// if no change in state  take the initial state time and add current state time
	if initialState == currentState && stateCount == 1 {
		currentStateDuration += states[stateCount-1].duration
		stateDurKey = a.GetKeyName(currentState, DurFieldSuffix)
		stateCountKey = a.GetKeyName(currentState, CountFieldSuffix)
		// initialise the number of transitions if it's the 1st time
		_, containsState = a.AggState[stateDurKey]
		if !containsState {
			a.AggState[stateDurKey] = currentStateDuration
			a.AggState[stateCountKey] = 1
		} else {
			a.AggState[stateDurKey] = currentStateDuration
		}

	} else {
		// current state time is the last state time
		currentStateDuration = states[stateCount-1].duration
		// calc the total duration and number of transitions in each state.
		for i := 0; i < stateCount; i++ {
			// if first occurance of state add with initial duration and a single transition
			stateDurKey = a.GetKeyName(states[i].name, DurFieldSuffix)
			stateCountKey = a.GetKeyName(states[i].name, CountFieldSuffix)
			_, containsState = a.AggState[stateDurKey]
			if !containsState {
				a.AggState[stateCountKey] = 1
				a.AggState[stateDurKey] = states[i].duration
			} else {
				// increment number of times in the state
				a.AggState[stateCountKey]++
				// add state time to aggregate total
				a.AggState[stateDurKey] += states[i].duration
			}
		}
	}

	a.CurrentState = currentState
	a.CurrentStateDuration = currentStateDuration
}
