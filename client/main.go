package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cactus/go-statsd-client/v5/statsd"
)

// SystemSpecs represents system specifications
type SystemSpecs struct {
	CPUCores     int    `json:"cpu_cores"`
	Memory       string `json:"memory"`
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	GoVersion    string `json:"go_version"`
	Hostname     string `json:"hostname"`
	Network      string `json:"network"`
}

// GetSystemSpecs collects system specifications
func GetSystemSpecs() *SystemSpecs {
	hostname, _ := os.Hostname()

	specs := &SystemSpecs{
		CPUCores:     runtime.NumCPU(),
		OS:           runtime.GOOS,
		Architecture: runtime.GOARCH,
		GoVersion:    runtime.Version(),
		Hostname:     hostname,
	}

	// Get memory information (Linux specific)
	if runtime.GOOS == "linux" {
		if memInfo := getLinuxMemoryInfo(); memInfo != "" {
			specs.Memory = memInfo
		} else {
			specs.Memory = "Unknown"
		}
	} else {
		specs.Memory = "Unknown (non-Linux)"
	}

	// Get network information
	if networkInfo := getNetworkInfo(); networkInfo != "" {
		specs.Network = networkInfo
	} else {
		specs.Network = "Unknown"
	}

	return specs
}

// getLinuxMemoryInfo reads memory information from /proc/meminfo
func getLinuxMemoryInfo() string {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return ""
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "MemTotal:") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				memKB, err := strconv.Atoi(fields[1])
				if err == nil {
					memGB := float64(memKB) / 1024 / 1024
					return fmt.Sprintf("%.1f GB", memGB)
				}
			}
		}
	}
	return ""
}

// getNetworkInfo gets basic network interface information (Linux)
func getNetworkInfo() string {
	if runtime.GOOS != "linux" {
		return "N/A (non-Linux)"
	}

	data, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return "Unknown"
	}

	lines := strings.Split(string(data), "\n")
	var activeInterfaces []string

	for _, line := range lines {
		// Skip header lines
		if strings.Contains(line, "Inter-") || strings.Contains(line, "face") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 2 {
			interfaceName := strings.TrimSuffix(fields[0], ":")
			// Skip loopback and get only active interfaces with reasonable names
			if interfaceName != "lo" && !strings.HasPrefix(interfaceName, "docker") &&
				!strings.HasPrefix(interfaceName, "br-") && len(interfaceName) <= 10 {
				activeInterfaces = append(activeInterfaces, interfaceName)
			}
		}

		// Limit to first 3 interfaces to keep output clean
		if len(activeInterfaces) >= 3 {
			break
		}
	}

	if len(activeInterfaces) > 0 {
		return strings.Join(activeInterfaces, ", ")
	}
	return "Unknown"
}

// RunConcurrentTest is a helper function that wraps LoadTester usage
func RunConcurrentTest(config LoadTestConfig, metricPrefix string) (*LoadTestResult, error) {
	// Create StatsD client
	statsClient, err := statsd.New("127.0.0.1:8125", metricPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to create StatsD client: %v", err)
	}
	defer statsClient.Close()

	// Create LoadTester
	loadTester := NewLoadTester(config, statsClient)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()

	// Run the test
	return loadTester.RunConcurrentTest(ctx)
}

// RunStabilityTest implements gradual ramp-up stability testing
func RunStabilityTest(scenario Scenario, metricPrefix string) (*LoadTestResult, error) {
	log.Printf("Starting stability test with gradual ramp-up")
	log.Printf("Start Concurrency: %d, Target RPS: %d, Step: %d",
		scenario.StartConcurrency, scenario.TargetRPS, scenario.ConcurrencyStep)

	// Create StatsD client
	statsClient, err := statsd.New("127.0.0.1:8125", metricPrefix)
	if err != nil {
		return nil, fmt.Errorf("failed to create StatsD client: %v", err)
	}
	defer statsClient.Close()

	// Start tracking actual test time
	actualStartTime := time.Now()

	// Aggregate results
	totalResult := &LoadTestResult{
		MinLatency: 1 * time.Hour, // Initialize with large value
		ActualStartTime: actualStartTime,
	}

	// Track total latency for correct average calculation
	var totalLatencySum time.Duration

	currentConcurrency := scenario.StartConcurrency
	stepNumber := 1

	// Phase 1: Ramp up from start_concurrency to target_rps in steps
	log.Printf("=== PHASE 1: RAMP-UP ===")
	for currentConcurrency <= scenario.TargetRPS {
		log.Printf("\n--- Ramp-Up Step %d: %d workers ---", stepNumber, currentConcurrency)

		// For stability test, run for a fixed duration at each step
		stepDuration := 30 * time.Second // 30 seconds per step

		// Calculate requests for this step (simulate continuous load)
		requestsForStep := int(float64(currentConcurrency) * stepDuration.Seconds())

		stepConfig := LoadTestConfig{
			URL:           scenario.GetURLWithPayloadSize(),
			TotalRequests: requestsForStep,
			Concurrency:   currentConcurrency,
			Duration:      stepDuration,
			RampUpTime:    0,
			RequestDelay:  0,
		}

		// Create LoadTester for this step
		loadTester := NewLoadTester(stepConfig, statsClient)

		// Create context with timeout for this step
		ctx, cancel := context.WithTimeout(context.Background(), stepDuration+10*time.Second)

		// Run this step
		stepResult, err := loadTester.RunConcurrentTest(ctx)
		cancel()

		if err != nil {
			log.Printf("Error in ramp-up step %d: %v", stepNumber, err)
			return nil, err
		}

		// Aggregate results
		totalResult.TotalRequests += stepResult.TotalRequests
		totalResult.SuccessRequests += stepResult.SuccessRequests
		totalResult.FailedRequests += stepResult.FailedRequests

		// Accumulate total latency for correct average calculation
		totalLatencySum += stepResult.AverageLatency * time.Duration(stepResult.TotalRequests)

		// Update latency stats
		if stepResult.MinLatency < totalResult.MinLatency {
			totalResult.MinLatency = stepResult.MinLatency
		}
		if stepResult.MaxLatency > totalResult.MaxLatency {
			totalResult.MaxLatency = stepResult.MaxLatency
		}

		// Add step result for detailed tracking
		stepResultDetail := StepResult{
			StepNumber:      stepNumber,
			StepType:        "ramp-up",
			Concurrency:     currentConcurrency,
			StepDuration:    stepResult.TestDuration,
			TotalRequests:   stepResult.TotalRequests,
			SuccessRequests: stepResult.SuccessRequests,
			FailedRequests:  stepResult.FailedRequests,
			RequestsPerSec:  stepResult.RequestsPerSec,
			AverageLatency:  stepResult.AverageLatency,
			MinLatency:      stepResult.MinLatency,
			MaxLatency:      stepResult.MaxLatency,
		}
		totalResult.StepResults = append(totalResult.StepResults, stepResultDetail)

		log.Printf("Ramp-Up Step %d completed: %d requests, %.2f RPS, avg latency %v",
			stepNumber, stepResult.TotalRequests, stepResult.RequestsPerSec, stepResult.AverageLatency)

		// Move to next step
		currentConcurrency += scenario.ConcurrencyStep
		stepNumber++
	}

	// Phase 2: Hold at target concurrency for stability testing
	if scenario.StabilityHoldCount > 0 {
		log.Printf("\n=== PHASE 2: STABILITY HOLD (Target: %d workers) ===", scenario.TargetRPS)

		for holdCycle := 1; holdCycle <= scenario.StabilityHoldCount; holdCycle++ {
			log.Printf("\n--- Stability Hold Cycle %d/%d: %d workers ---",
				holdCycle, scenario.StabilityHoldCount, scenario.TargetRPS)

			// Run at target concurrency for stability measurement
			holdDuration := 45 * time.Second // Longer duration for stability measurement
			requestsForHold := int(float64(scenario.TargetRPS) * holdDuration.Seconds())

			holdConfig := LoadTestConfig{
				URL:           scenario.GetURLWithPayloadSize(),
				TotalRequests: requestsForHold,
				Concurrency:   scenario.TargetRPS,
				Duration:      holdDuration,
				RampUpTime:    0,
				RequestDelay:  0,
			}

			// Create LoadTester for this hold cycle
			loadTester := NewLoadTester(holdConfig, statsClient)

			// Create context with timeout for this hold cycle
			ctx, cancel := context.WithTimeout(context.Background(), holdDuration+15*time.Second)

			// Run this hold cycle
			holdResult, err := loadTester.RunConcurrentTest(ctx)
			cancel()

			if err != nil {
				log.Printf("Error in stability hold cycle %d: %v", holdCycle, err)
				return nil, err
			}

			// Aggregate results
			totalResult.TotalRequests += holdResult.TotalRequests
			totalResult.SuccessRequests += holdResult.SuccessRequests
			totalResult.FailedRequests += holdResult.FailedRequests

			// Accumulate total latency for correct average calculation
			totalLatencySum += holdResult.AverageLatency * time.Duration(holdResult.TotalRequests)

			// Update latency stats
			if holdResult.MinLatency < totalResult.MinLatency {
				totalResult.MinLatency = holdResult.MinLatency
			}
			if holdResult.MaxLatency > totalResult.MaxLatency {
				totalResult.MaxLatency = holdResult.MaxLatency
			}

			// Add hold result for detailed tracking
			holdResultDetail := StepResult{
				StepNumber:      holdCycle,
				StepType:        "hold",
				Concurrency:     scenario.TargetRPS,
				StepDuration:    holdResult.TestDuration,
				TotalRequests:   holdResult.TotalRequests,
				SuccessRequests: holdResult.SuccessRequests,
				FailedRequests:  holdResult.FailedRequests,
				RequestsPerSec:  holdResult.RequestsPerSec,
				AverageLatency:  holdResult.AverageLatency,
				MinLatency:      holdResult.MinLatency,
				MaxLatency:      holdResult.MaxLatency,
			}
			totalResult.StepResults = append(totalResult.StepResults, holdResultDetail)

			log.Printf("Stability Hold Cycle %d completed: %d requests, %.2f RPS, avg latency %v",
				holdCycle, holdResult.TotalRequests, holdResult.RequestsPerSec, holdResult.AverageLatency)
		}
	}

	// Set actual end time and calculate final aggregated stats
	actualEndTime := time.Now()
	actualTestDuration := actualEndTime.Sub(actualStartTime)

	totalResult.ActualEndTime = actualEndTime
	totalResult.TestDuration = actualTestDuration

	if totalResult.TotalRequests > 0 {
		// Correct average latency calculation using accumulated latency sum
		totalResult.AverageLatency = totalLatencySum / time.Duration(totalResult.TotalRequests)

		// Correct RPS calculation using actual test duration
		totalResult.RequestsPerSec = float64(totalResult.TotalRequests) / actualTestDuration.Seconds()

		totalResult.ErrorRate = float64(totalResult.FailedRequests) / float64(totalResult.TotalRequests) * 100
	}

	totalPhases := 1
	if scenario.StabilityHoldCount > 0 {
		totalPhases = 2
	}
	log.Printf("Stability test completed: %d phases (%d ramp-up steps + %d hold cycles), %d total requests",
		totalPhases, stepNumber-1, scenario.StabilityHoldCount, totalResult.TotalRequests)

	// Validate metrics for consistency
	if err := totalResult.ValidateMetrics(); err != nil {
		log.Printf("Warning: Metric validation failed: %v", err)
	} else {
		log.Printf("✅ Metric validation passed")
	}

	return totalResult, nil
}

func main() {
	// Define command line flags
	var scenarioName string
	var listScenarios bool
	// var gateway string 
	var configFile string 
	var targetName string

	flag.StringVar(&scenarioName, "scenario", "", "Run specific scenario by name")
	flag.BoolVar(&listScenarios, "list", false, "List all available scenarios")
	// flag.StringVar(&gateway, "gateway", "envoy", "Choose gateway: envoy | kong | backend") 
	flag.StringVar(&targetName, "target", "backend", "Target API: backend | kong | envoy")
	flag.StringVar(&configFile, "config", "./test-config.json", "Path to test config file")
	flag.Parse()

	gateway := targetName

	// Load test configuration from JSON file (our single source of truth)
	config, err := LoadTestConfigFromJSON(configFile)
	if err != nil {
		log.Fatalf("Failed to load test configuration: %v", err)
	}

	log.Printf("Loaded test configuration: %s", config.Name)
	log.Printf("Description: %s", config.Description)

	allScenarios := config.GetAllScenarios()
	log.Printf("Number of scenarios: %d (LoadTest: %d, Stability: %d)",
		len(allScenarios), len(config.LoadTestScenarios), len(config.StabilityTestScenarios))

	// List scenarios if requested
	if listScenarios {
		fmt.Println("\nAvailable scenarios:")
		fmt.Println("=== LoadTest Scenarios ===")
		for i, scenario := range config.LoadTestScenarios {
			fmt.Printf("  %d. %s (%s)\n", i+1, scenario.Name, scenario.DisplayName)
			fmt.Printf("     Description: %s\n", scenario.Description)
			fmt.Printf("     Requests: %d, Concurrency: %d, Target RPS: %d\n\n",
				scenario.TotalRequests, scenario.Concurrency, scenario.TargetRPS)
		}
		fmt.Println("=== Stability Test Scenarios ===")
		for i, scenario := range config.StabilityTestScenarios {
			fmt.Printf("  %d. %s (%s)\n", len(config.LoadTestScenarios)+i+1, scenario.Name, scenario.DisplayName)
			fmt.Printf("     Description: %s\n", scenario.Description)
			fmt.Printf("     Target RPS: %d, Start Concurrency: %d, Step: %d\n\n",
				scenario.TargetRPS, scenario.StartConcurrency, scenario.ConcurrencyStep)
		}
		return
	}

	// Filter scenarios based on command line argument
	var scenariosToRun []Scenario
	if scenarioName != "" {
		// Find specific scenario in all scenarios
		found := false
		for _, scenario := range allScenarios {
			if scenario.Name == scenarioName {
				scenariosToRun = append(scenariosToRun, scenario)
				found = true
				break
			}
		}
		if !found {
			log.Fatalf("Scenario '%s' not found. Use --list to see available scenarios.", scenarioName)
		}
		log.Printf("Running specific scenario: %s", scenarioName)
	} else {
		// Run all scenarios
		scenariosToRun = allScenarios
		log.Printf("Running all %d scenarios", len(scenariosToRun))
	}

	// Execute scenarios
	for i, scenario := range scenariosToRun {
		log.Printf("\n=== Running Scenario %d/%d: %s ===", i+1, len(scenariosToRun), scenario.Name)
		log.Printf("Display Name: %s", scenario.DisplayName)
		log.Printf("Description: %s", scenario.Description)

		// Display system specifications at the start of each test
		specs := GetSystemSpecs()
		log.Printf("\n🖥️  System Specifications:")
		log.Printf("   Hostname: %s", specs.Hostname)
		log.Printf("   CPU Cores: %d cores", specs.CPUCores)
		log.Printf("   Memory: %s", specs.Memory)
		log.Printf("   OS: %s/%s", specs.OS, specs.Architecture)
		log.Printf("   Go Version: %s", specs.GoVersion)
		log.Printf("   Network Interfaces: %s", specs.Network)

		// Convert scenario to LoadTestConfig
		loadTestConfig := scenario.ConvertToLoadTestConfig()

		// Override URL berdasarkan gateway flag
		 switch gateway {
        case "envoy":
            loadTestConfig.URL = "http://localhost:8082/hello"
        case "kong":
            loadTestConfig.URL = "http://localhost:8000/hello"
        case "traefik":
            loadTestConfig.URL = "http://localhost:9000/hello"
        case "backend":
            loadTestConfig.URL = "http://localhost:9001/hello"
        default:
            log.Fatalf("Unknown gateway: %s", gateway)
        }


		log.Printf("Configuration:")
		log.Printf("  URL: %s", loadTestConfig.URL)
		log.Printf("  Total Requests: %d", loadTestConfig.TotalRequests)
		log.Printf("  Concurrency: %d", loadTestConfig.Concurrency)
		log.Printf("  Duration: %s", loadTestConfig.Duration)

		// Create metric prefix for this scenario
		metricPrefix := fmt.Sprintf("loadtest.%s.%s", targetName, scenario.Name)

		var results *LoadTestResult
		var err error

		// Determine if this is a stability test or load test
		if strings.HasPrefix(scenario.Name, "stability_") {
			// Run stability test with gradual ramp-up
			results, err = RunStabilityTest(scenario, metricPrefix)
		} else {
			// Run regular load test
			results, err = RunConcurrentTest(loadTestConfig, metricPrefix)
		}
		
		if err != nil {
			log.Printf("Error running scenario %s: %v", scenario.Name, err)
			continue
		}

		// Display results with enhanced analysis for stability tests
		if strings.HasPrefix(scenario.Name, "stability_") {
			// Convert to enhanced stability test result
			stabilityResult := results.ToStabilityTestResult()

			// Print enhanced stability results
			PrintStabilityResults(stabilityResult)

			// Log key metrics for summary
			log.Printf("\n=== Enhanced Stability Results for %s ===", scenario.Name)
			log.Printf("Peak Throughput: %.1f RPS", stabilityResult.PeakThroughput)
			log.Printf("Sustained Throughput: %.1f RPS (Hold Phase Average)", stabilityResult.SustainedThroughput)
			log.Printf("Overall Average: %.1f RPS (Including Ramp-up)", stabilityResult.OverallMetrics.RequestsPerSec)

			efficiency := (stabilityResult.SustainedThroughput / stabilityResult.PeakThroughput) * 100
			log.Printf("Sustained Efficiency: %.1f%%", efficiency)

			if stabilityResult.HoldMetrics != nil {
				log.Printf("Hold Phase Error Rate: %.2f%%", stabilityResult.HoldMetrics.ErrorRate)
				log.Printf("Hold Phase Duration: %v", stabilityResult.HoldMetrics.Duration.Truncate(time.Second))
			}

		} else {
			// Display regular load test results
			log.Printf("\n=== Load Test Results for %s ===", scenario.Name)
			log.Printf("Total Requests: %d", results.TotalRequests)
			log.Printf("Successful Requests: %d", results.SuccessRequests)
			log.Printf("Failed Requests: %d", results.FailedRequests)
			log.Printf("Average Latency: %.3fµs", float64(results.AverageLatency.Nanoseconds())/1000.0)
			log.Printf("Min Latency: %.3fµs", float64(results.MinLatency.Nanoseconds())/1000.0)
			log.Printf("Max Latency: %.3fµs", float64(results.MaxLatency.Nanoseconds())/1000.0)
			log.Printf("Throughput: %.2f RPS", results.RequestsPerSec)
			log.Printf("Test Duration: %v", results.TestDuration)
		}
	}

	fmt.Printf("\nAll scenarios completed. Total scenarios run: %d\n", len(scenariosToRun))
}

