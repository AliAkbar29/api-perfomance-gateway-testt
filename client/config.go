package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// TestConfig represents the overall test configuration - matches test-config.json
type TestConfig struct {
	Name                   string     `json:"name"`
	Description            string     `json:"description"`
	LoadTestScenarios      []Scenario `json:"loadtest_scenarios"`
	StabilityTestScenarios []Scenario `json:"stabilitytest_scenarios"`
	Metadata               Metadata   `json:"metadata"`
}

// Scenario represents a single test scenario (unified for both types)
type Scenario struct {
	Name             string `json:"name"`
	DisplayName      string `json:"display_name"`
	URL              string `json:"url"`
	TotalRequests    int    `json:"total_requests"`    // Used by load tests
	Concurrency      int    `json:"concurrency"`       // Used by load tests
	Duration         string `json:"duration"`          // Used by load tests (if specified)
	TargetRPS           int    `json:"target_rps"`           // Used by both
	StartConcurrency    int    `json:"start_concurrency"`   // Used by stability tests
	ConcurrencyStep     int    `json:"concurrency_step"`    // Used by stability tests
	StabilityHoldCount  int    `json:"stability_hold_count"` // Used by stability tests - cycles to hold at target
	PayloadSizeKB       int    `json:"payload_size_kb"`      // Payload size in KB for backend response
	Description         string `json:"description"`
}

// Metadata represents test suite metadata
type Metadata struct {
	Version string `json:"version"`
	Created string `json:"created"`
	Author  string `json:"author"`
	Notes   string `json:"notes"`
}

// PhaseMetrics represents metrics for a specific test phase
type PhaseMetrics struct {
	PhaseName       string        `json:"phase_name"`
	Duration        time.Duration `json:"duration"`
	TotalRequests   int           `json:"total_requests"`
	SuccessRequests int           `json:"success_requests"`
	FailedRequests  int           `json:"failed_requests"`
	RequestsPerSec  float64       `json:"requests_per_sec"`
	AverageLatency  time.Duration `json:"average_latency"`
	MinLatency      time.Duration `json:"min_latency"`
	MaxLatency      time.Duration `json:"max_latency"`
	ErrorRate       float64       `json:"error_rate"`
}

// StabilityTestResult represents enhanced results for stability tests
type StabilityTestResult struct {
	RampUpMetrics    *PhaseMetrics   `json:"ramp_up_metrics"`
	HoldMetrics      *PhaseMetrics   `json:"hold_metrics"`
	OverallMetrics   *PhaseMetrics   `json:"overall_metrics"`
	PeakThroughput   float64         `json:"peak_throughput"`
	SustainedThroughput float64      `json:"sustained_throughput"`
	StepResults      []StepResult    `json:"step_results"`
	IsStabilityTest  bool            `json:"is_stability_test"`
}

// LoadTestConfigFromJSON loads test configuration from a JSON file
func LoadTestConfigFromJSON(filename string) (*TestConfig, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var config TestConfig
	decoder := json.NewDecoder(file)
	err = decoder.Decode(&config)
	if err != nil {
		return nil, err
	}

	return &config, nil
}

// GetAllScenarios combines load test and stability test scenarios into a single slice
func (tc *TestConfig) GetAllScenarios() []Scenario {
	all := make([]Scenario, 0, len(tc.LoadTestScenarios)+len(tc.StabilityTestScenarios))
	all = append(all, tc.LoadTestScenarios...)
	all = append(all, tc.StabilityTestScenarios...)
	return all
}

// ConvertToLoadTestConfig converts a Scenario to LoadTestConfig for concurrent_client.go
func (s *Scenario) ConvertToLoadTestConfig() LoadTestConfig {
	var duration time.Duration

	// Parse duration if specified, otherwise calculate a reasonable default
	if s.Duration != "" {
		duration, _ = time.ParseDuration(s.Duration)
	} else {
		// For load test scenarios without explicit duration, set based on total requests
		if s.TotalRequests > 0 && s.Concurrency > 0 {
			// Estimate time needed: (total_requests / concurrency) + buffer
			estimatedTime := (s.TotalRequests / s.Concurrency) + 30 // Add 30 second buffer
			if estimatedTime < 60 {
				estimatedTime = 60 // Minimum 1 minute
			}
			duration = time.Duration(estimatedTime) * time.Second
		} else {
			duration = 2 * time.Minute // Default 2 minutes
		}
	}

	return LoadTestConfig{
		URL:           s.GetURLWithPayloadSize(),
		TotalRequests: s.TotalRequests,
		Concurrency:   s.Concurrency,
		Duration:      duration,
		RampUpTime:    0, // Not used in current simple scenarios
		RequestDelay:  0, // Not used in current simple scenarios
	}
}

// GetURLWithPayloadSize builds the URL with payload size parameter
func (s *Scenario) GetURLWithPayloadSize() string {
	if s.PayloadSizeKB > 0 {
		return fmt.Sprintf("%s?size=%d", s.URL, s.PayloadSizeKB)
	}
	return s.URL // Default size (1KB) if not specified
}