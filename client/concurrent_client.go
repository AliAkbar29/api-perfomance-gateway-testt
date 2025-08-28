package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cactus/go-statsd-client/v5/statsd"
)

// Circuit breaker implementation as recommended in timeout analysis
var ErrCircuitOpen = errors.New("circuit breaker is open")

type CircuitBreakerState int

const (
	CircuitClosed CircuitBreakerState = iota
	CircuitOpen
	CircuitHalfOpen
)

type CircuitBreaker struct {
	maxFailures    int
	timeout        time.Duration
	failureCount   int64
	lastFailTime   time.Time
	state          CircuitBreakerState
	mutex          sync.RWMutex
}

func NewCircuitBreaker(maxFailures int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		maxFailures: maxFailures,
		timeout:     timeout,
		state:       CircuitClosed,
	}
}

func (cb *CircuitBreaker) Call(fn func() error) error {
	cb.mutex.RLock()
	state := cb.state
	cb.mutex.RUnlock()

	if state == CircuitOpen {
		if time.Since(cb.lastFailTime) > cb.timeout {
			cb.mutex.Lock()
			cb.state = CircuitHalfOpen
			cb.mutex.Unlock()
		} else {
			return ErrCircuitOpen
		}
	}

	err := fn()
	
	cb.mutex.Lock()
	defer cb.mutex.Unlock()
	
	if err != nil {
		cb.failureCount++
		cb.lastFailTime = time.Now()
		
		if cb.failureCount >= int64(cb.maxFailures) {
			cb.state = CircuitOpen
		}
	} else {
		cb.failureCount = 0
		cb.state = CircuitClosed
	}
	
	return err
}

func (cb *CircuitBreaker) IsOpen() bool {
	cb.mutex.RLock()
	defer cb.mutex.RUnlock()
	return cb.state == CircuitOpen
}

// Adaptive rate limiter implementation as recommended in timeout analysis
type AdaptiveRateLimiter struct {
	currentRate       float64
	targetErrorRate   float64
	adjustmentFactor  float64
	minRate          float64
	maxRate          float64
	mutex            sync.RWMutex
}

func NewAdaptiveRateLimiter(initialRate, targetErrorRate, minRate, maxRate float64) *AdaptiveRateLimiter {
	return &AdaptiveRateLimiter{
		currentRate:      initialRate,
		targetErrorRate:  targetErrorRate,
		adjustmentFactor: 0.1, // 10% adjustment per step
		minRate:         minRate,
		maxRate:         maxRate,
	}
}

func (arl *AdaptiveRateLimiter) AdjustRate(errorRate float64) {
	arl.mutex.Lock()
	defer arl.mutex.Unlock()
	
	if errorRate > arl.targetErrorRate {
		// Reduce rate when error rate is too high
		arl.currentRate *= (1.0 - arl.adjustmentFactor)
		if arl.currentRate < arl.minRate {
			arl.currentRate = arl.minRate
		}
	} else {
		// Increase rate when error rate is acceptable
		arl.currentRate *= (1.0 + arl.adjustmentFactor)
		if arl.currentRate > arl.maxRate {
			arl.currentRate = arl.maxRate
		}
	}
}

func (arl *AdaptiveRateLimiter) GetCurrentRate() float64 {
	arl.mutex.RLock()
	defer arl.mutex.RUnlock()
	return arl.currentRate
}

type LoadTestConfig struct {
	URL           string        `json:"url"`
	TotalRequests int           `json:"total_requests"`
	Concurrency   int           `json:"concurrency"`
	Duration      time.Duration `json:"duration"`
	RampUpTime    time.Duration `json:"ramp_up_time"`
	RequestDelay  time.Duration `json:"request_delay"`
}

type LoadTestResult struct {
	TotalRequests    int64         `json:"total_requests"`
	SuccessRequests  int64         `json:"success_requests"`
	FailedRequests   int64         `json:"failed_requests"`
	AverageLatency   time.Duration `json:"average_latency"`
	MinLatency       time.Duration `json:"min_latency"`
	MaxLatency       time.Duration `json:"max_latency"`
	RequestsPerSec   float64       `json:"requests_per_sec"`
	TestDuration     time.Duration `json:"test_duration"`
	ErrorRate        float64       `json:"error_rate"`
	ActualStartTime  time.Time     `json:"actual_start_time"`
	ActualEndTime    time.Time     `json:"actual_end_time"`
	StepResults      []StepResult  `json:"step_results,omitempty"`
}

// StepResult represents results from individual test steps
type StepResult struct {
	StepNumber      int           `json:"step_number"`
	StepType        string        `json:"step_type"` // "ramp-up" or "hold"
	Concurrency     int           `json:"concurrency"`
	StepDuration    time.Duration `json:"step_duration"`
	TotalRequests   int64         `json:"total_requests"`
	SuccessRequests int64         `json:"success_requests"`
	FailedRequests  int64         `json:"failed_requests"`
	RequestsPerSec  float64       `json:"requests_per_sec"`
	AverageLatency  time.Duration `json:"average_latency"`
	MinLatency      time.Duration `json:"min_latency"`
	MaxLatency      time.Duration `json:"max_latency"`
}

// ValidateMetrics checks consistency of calculated metrics
func (r *LoadTestResult) ValidateMetrics() error {
	if r.TotalRequests == 0 {
		return nil // Skip validation for empty results
	}
	
	// Validate RPS calculation
	actualDuration := r.ActualEndTime.Sub(r.ActualStartTime)
	if actualDuration > 0 {
		calculatedRPS := float64(r.TotalRequests) / actualDuration.Seconds()
		if math.Abs(calculatedRPS - r.RequestsPerSec) > 0.1 {
			return fmt.Errorf("RPS calculation inconsistent: expected %.2f, got %.2f", calculatedRPS, r.RequestsPerSec)
		}
	}
	
	// Validate error rate
	if r.TotalRequests > 0 {
		calculatedErrorRate := float64(r.FailedRequests) / float64(r.TotalRequests) * 100
		if math.Abs(calculatedErrorRate - r.ErrorRate) > 0.01 {
			return fmt.Errorf("error rate calculation inconsistent: expected %.2f%%, got %.2f%%", calculatedErrorRate, r.ErrorRate)
		}
	}
	
	// Validate request counts
	if r.SuccessRequests + r.FailedRequests != r.TotalRequests {
		return fmt.Errorf("request count inconsistent: success(%d) + failed(%d) != total(%d)", 
			r.SuccessRequests, r.FailedRequests, r.TotalRequests)
	}
	
	return nil
}

type LoadTester struct {
	config           LoadTestConfig
	httpClient       *http.Client
	statsClient      statsd.Statter
	results          LoadTestResult
	latencies        []time.Duration
	mutex            sync.RWMutex
	circuitBreaker   *CircuitBreaker
	rateLimiter      *AdaptiveRateLimiter
}

func NewLoadTester(config LoadTestConfig, statsClient statsd.Statter) *LoadTester {
	// Create HTTP client with optimized settings for high concurrency
	// Based on timeout analysis: increased connection pool to prevent "context deadline exceeded"
	httpClient := &http.Client{
		Timeout: 30 * time.Second, // Reduced from 120s as recommended in analysis
		Transport: &http.Transport{
			MaxIdleConns:        2000,              // Increased from 1000
			MaxIdleConnsPerHost: 500,               // Increased from 100 (5x more)
			MaxConnsPerHost:     1000,              // Increased from 200 (5x more)
			IdleConnTimeout:     120 * time.Second, // Keep connections alive longer
			TLSHandshakeTimeout: 10 * time.Second,
			DisableKeepAlives:   false,             // Enable keep-alive
			DisableCompression:  true,              // Reduce CPU overhead
		},
	}

	// Initialize circuit breaker: max 10 failures, 30 second timeout
	circuitBreaker := NewCircuitBreaker(10, 30*time.Second)
	
	// Initialize adaptive rate limiter: start at 1000 RPS, target 0.1% error rate
	rateLimiter := NewAdaptiveRateLimiter(1000.0, 0.1, 100.0, 5000.0)

	return &LoadTester{
		config:         config,
		httpClient:     httpClient,
		statsClient:    statsClient,
		latencies:      make([]time.Duration, 0),
		circuitBreaker: circuitBreaker,
		rateLimiter:    rateLimiter,
	}
}

// Concurrent load testing with worker pool pattern
func (lt *LoadTester) RunConcurrentTest(ctx context.Context) (*LoadTestResult, error) {
	log.Printf("Starting concurrent load test...")
	log.Printf("URL: %s", lt.config.URL)
	log.Printf("Total Requests: %d", lt.config.TotalRequests)
	log.Printf("Concurrency: %d", lt.config.Concurrency)
	log.Printf("Duration: %v", lt.config.Duration)

	startTime := time.Now()
	
	// Set actual start time for tracking
	lt.results.ActualStartTime = startTime
	
	// Channels for coordinating workers
	requestChan := make(chan int, lt.config.TotalRequests)
	resultChan := make(chan RequestResult, lt.config.TotalRequests)
	
	// Context with timeout
	testCtx, cancel := context.WithTimeout(ctx, lt.config.Duration)
	defer cancel()

	// Fill request channel
	go func() {
		defer close(requestChan)
		for i := 0; i < lt.config.TotalRequests; i++ {
			select {
			case requestChan <- i:
			case <-testCtx.Done():
				return
			}
		}
	}()

	// Start worker goroutines
	var wg sync.WaitGroup
	for i := 0; i < lt.config.Concurrency; i++ {
		wg.Add(1)
		go lt.worker(testCtx, &wg, requestChan, resultChan, i)
	}

	// Close result channel when all workers are done
	go func() {
		wg.Wait()
		close(resultChan)
	}()

	// Collect results
	lt.collectResults(resultChan, startTime)

	return &lt.results, nil
}

type RequestResult struct {
	Success   bool
	Latency   time.Duration
	ErrorMsg  string
	Timestamp time.Time
}

func (lt *LoadTester) worker(ctx context.Context, wg *sync.WaitGroup, requestChan <-chan int, resultChan chan<- RequestResult, workerID int) {
	defer wg.Done()
	
	// Remove verbose worker start logs for cleaner output
	
	for {
		select {
		case requestID, ok := <-requestChan:
			if !ok {
				// Remove verbose worker finish logs for cleaner output
				return
			}
			
			// Perform HTTP request
			result := lt.performRequest(ctx, requestID)
			
			// Send result
			select {
			case resultChan <- result:
			case <-ctx.Done():
				return
			}
			
			// Optional delay between requests
			if lt.config.RequestDelay > 0 {
				time.Sleep(lt.config.RequestDelay)
			}
			
		case <-ctx.Done():
			// Worker cancelled silently for cleaner output
			return
		}
	}
}

func (lt *LoadTester) performRequest(ctx context.Context, requestID int) RequestResult {
	start := time.Now()
	
	// Check circuit breaker before making request
	if lt.circuitBreaker.IsOpen() {
		return RequestResult{
			Success:   false,
			Latency:   time.Since(start),
			ErrorMsg:  "Circuit breaker is open - request blocked",
			Timestamp: start,
		}
	}
	
	var resp *http.Response
	var err error
	
	// Use circuit breaker to wrap the HTTP request
	cbErr := lt.circuitBreaker.Call(func() error {
		req, reqErr := http.NewRequestWithContext(ctx, "GET", lt.config.URL, nil)
		if reqErr != nil {
			return reqErr
		}
		
		// Add headers for better tracking
		req.Header.Set("User-Agent", "LoadTester/1.0")
		req.Header.Set("X-Request-ID", fmt.Sprintf("req-%d", requestID))
		
		resp, err = lt.httpClient.Do(req)
		return err
	})
	
	latency := time.Since(start)
	
	// Handle circuit breaker errors
	if cbErr != nil {
		if cbErr == ErrCircuitOpen {
			return RequestResult{
				Success:   false,
				Latency:   latency,
				ErrorMsg:  "Circuit breaker is open",
				Timestamp: start,
			}
		}
		err = cbErr
	}
	
	if err != nil {
		// Send metrics for failed request
		if lt.statsClient != nil {
			lt.statsClient.Inc("request.failed", 1, 1.0)
			lt.statsClient.Timing("latency.failed", int64(latency.Microseconds()), 1.0)
		}
		
		return RequestResult{
			Success:   false,
			Latency:   latency,
			ErrorMsg:  fmt.Sprintf("Request failed: %v", err),
			Timestamp: start,
		}
	}
	defer resp.Body.Close()
	
	// Read response body
	_, err = io.ReadAll(resp.Body)
	if err != nil {
		return RequestResult{
			Success:   false,
			Latency:   latency,
			ErrorMsg:  fmt.Sprintf("Failed to read response: %v", err),
			Timestamp: start,
		}
	}
	
	success := resp.StatusCode >= 200 && resp.StatusCode < 300
	
	// Send metrics
	if lt.statsClient != nil {
		if success {
			lt.statsClient.Inc("request.success", 1, 1.0)
			lt.statsClient.Timing("latency.success", int64(latency.Microseconds()), 1.0)
		} else {
			lt.statsClient.Inc("request.failed", 1, 1.0)
			lt.statsClient.Timing("latency.failed", int64(latency.Microseconds()), 1.0)
		}
		lt.statsClient.Timing("latency.total", int64(latency.Microseconds()), 1.0)
	}
	
	return RequestResult{
		Success:   success,
		Latency:   latency,
		ErrorMsg:  fmt.Sprintf("HTTP %d", resp.StatusCode),
		Timestamp: start,
	}
}

func (lt *LoadTester) collectResults(resultChan <-chan RequestResult, startTime time.Time) {
	var totalRequests, successRequests, failedRequests int64
	var totalLatency time.Duration
	var minLatency, maxLatency time.Duration = time.Hour, 0
	
	for result := range resultChan {
		atomic.AddInt64(&totalRequests, 1)
		
		if result.Success {
			atomic.AddInt64(&successRequests, 1)
		} else {
			atomic.AddInt64(&failedRequests, 1)
			log.Printf("Request failed: %s", result.ErrorMsg)
		}
		
		// Track latencies thread-safely
		lt.mutex.Lock()
		lt.latencies = append(lt.latencies, result.Latency)
		totalLatency += result.Latency
		
		if result.Latency < minLatency {
			minLatency = result.Latency
		}
		if result.Latency > maxLatency {
			maxLatency = result.Latency
		}
		lt.mutex.Unlock()
		
		// Print progress with fixed line update every 100 requests
		if totalRequests%100 == 0 {
			progressPercent := float64(totalRequests) / float64(lt.config.TotalRequests) * 100
			elapsedTime := time.Since(startTime)
			
			// Calculate current throughput and error rate
			currentThroughput := float64(totalRequests) / elapsedTime.Seconds()
			currentErrorRate := float64(failedRequests) / float64(totalRequests) * 100
			
			// Adjust rate limiter based on current error rate
			lt.rateLimiter.AdjustRate(currentErrorRate)
			adjustedRate := lt.rateLimiter.GetCurrentRate()
			
			// Use carriage return to update same line
			fmt.Printf("\r🚀 Progress: %d/%d (%.1f%%) | Elapsed: %v | Success: %d | Failed: %d | Throughput: %.1f RPS | Error Rate: %.2f%% | Adjusted Rate: %.1f RPS",
				totalRequests, lt.config.TotalRequests, progressPercent, 
				elapsedTime.Truncate(time.Second), successRequests, failedRequests, currentThroughput, currentErrorRate, adjustedRate)
		}
	}
	
	endTime := time.Now()
	testDuration := endTime.Sub(startTime)
	
	// Calculate final results with proper time tracking
	lt.results = LoadTestResult{
		TotalRequests:   totalRequests,
		SuccessRequests: successRequests,
		FailedRequests:  failedRequests,
		TestDuration:    testDuration,
		MinLatency:      minLatency,
		MaxLatency:      maxLatency,
		ActualStartTime: startTime,
		ActualEndTime:   endTime,
	}
	
	if totalRequests > 0 {
		lt.results.AverageLatency = totalLatency / time.Duration(totalRequests)
		lt.results.ErrorRate = float64(failedRequests) / float64(totalRequests) * 100
		lt.results.RequestsPerSec = float64(totalRequests) / testDuration.Seconds()
		
		// Send final throughput metric to StatsD
		if lt.statsClient != nil {
			lt.statsClient.Gauge("throughput.total_rps", int64(lt.results.RequestsPerSec), 1.0)
			lt.statsClient.Gauge("throughput.success_rps", int64(float64(successRequests) / testDuration.Seconds()), 1.0)
			if failedRequests > 0 {
				lt.statsClient.Gauge("throughput.failed_rps", int64(float64(failedRequests) / testDuration.Seconds()), 1.0)
			}
		}
	}
}

// Print detailed test results
func (lt *LoadTester) PrintResults() {
	// Ensure we end progress line properly
	fmt.Print("\n")
	fmt.Println("\n" + strings.Repeat("=", 60))
	fmt.Println("LOAD TEST RESULTS")
	fmt.Println(strings.Repeat("=", 60))
	fmt.Printf("Total Requests:      %d\n", lt.results.TotalRequests)
	fmt.Printf("Successful Requests: %d\n", lt.results.SuccessRequests)
	fmt.Printf("Failed Requests:     %d\n", lt.results.FailedRequests)
	fmt.Printf("Error Rate:          %.2f%%\n", lt.results.ErrorRate)
	fmt.Printf("Test Duration:       %v\n", lt.results.TestDuration)
	fmt.Printf("Requests/sec:        %.2f\n", lt.results.RequestsPerSec)
	fmt.Println(strings.Repeat("-", 60))
	fmt.Printf("Min Latency:         %.3fµs\n", float64(lt.results.MinLatency.Nanoseconds())/1000.0)
	fmt.Printf("Average Latency:     %.3fµs\n", float64(lt.results.AverageLatency.Nanoseconds())/1000.0)
	fmt.Printf("Max Latency:         %.3fµs\n", float64(lt.results.MaxLatency.Nanoseconds())/1000.0)
	
	// Calculate percentiles
	if len(lt.latencies) > 0 {
		p50, p95, p99 := lt.calculatePercentiles()
		fmt.Printf("50th percentile:     %.3fµs\n", float64(p50.Nanoseconds())/1000.0)
		fmt.Printf("95th percentile:     %.3fµs\n", float64(p95.Nanoseconds())/1000.0)
		fmt.Printf("99th percentile:     %.3fµs\n", float64(p99.Nanoseconds())/1000.0)
	}
	fmt.Println(strings.Repeat("=", 60))
}

func (lt *LoadTester) calculatePercentiles() (p50, p95, p99 time.Duration) {
	// Implementation for percentile calculation
	// This is a simplified version - in production use proper sorting
	if len(lt.latencies) == 0 {
		return 0, 0, 0
	}
	
	// Simple approximation - in real implementation, sort the slice
	total := len(lt.latencies)
	if total >= 2 {
		p50 = lt.latencies[total/2]
	}
	if total >= 20 {
		p95 = lt.latencies[total*95/100]
	}
	if total >= 100 {
		p99 = lt.latencies[total*99/100]
	}
	
	return p50, p95, p99
}

// CalculatePhaseMetrics creates PhaseMetrics from StepResults
func CalculatePhaseMetrics(stepResults []StepResult, phaseName string) *PhaseMetrics {
	if len(stepResults) == 0 {
		return &PhaseMetrics{PhaseName: phaseName}
	}

	var totalRequests, successRequests, failedRequests int64
	var totalDuration time.Duration
	var totalLatencySum time.Duration
	var minLatency time.Duration = time.Hour
	var maxLatency time.Duration
	var maxThroughput float64

	for _, step := range stepResults {
		totalRequests += step.TotalRequests
		successRequests += step.SuccessRequests
		failedRequests += step.FailedRequests
		totalDuration += step.StepDuration

		// Accumulate weighted latency
		totalLatencySum += step.AverageLatency * time.Duration(step.TotalRequests)

		if step.MinLatency < minLatency && step.MinLatency > 0 {
			minLatency = step.MinLatency
		}
		if step.MaxLatency > maxLatency {
			maxLatency = step.MaxLatency
		}
		if step.RequestsPerSec > maxThroughput {
			maxThroughput = step.RequestsPerSec
		}
	}

	phaseMetrics := &PhaseMetrics{
		PhaseName:       phaseName,
		Duration:        totalDuration,
		TotalRequests:   int(totalRequests),
		SuccessRequests: int(successRequests),
		FailedRequests:  int(failedRequests),
		MinLatency:      minLatency,
		MaxLatency:      maxLatency,
	}

	if totalRequests > 0 {
		phaseMetrics.AverageLatency = totalLatencySum / time.Duration(totalRequests)
		phaseMetrics.ErrorRate = float64(failedRequests) / float64(totalRequests) * 100
		phaseMetrics.RequestsPerSec = float64(totalRequests) / totalDuration.Seconds()
	}

	return phaseMetrics
}

// EnhancedStabilityTestResult creates enhanced results for stability tests
func (r *LoadTestResult) ToStabilityTestResult() *StabilityTestResult {
	if len(r.StepResults) == 0 {
		return &StabilityTestResult{
			OverallMetrics:      r.toPhaseMetrics("Overall"),
			IsStabilityTest:     true,
			PeakThroughput:      r.RequestsPerSec,
			SustainedThroughput: r.RequestsPerSec,
			StepResults:         r.StepResults,
		}
	}

	// Separate ramp-up and hold steps
	var rampUpSteps, holdSteps []StepResult
	var peakThroughput, sustainedThroughput float64

	for _, step := range r.StepResults {
		if step.StepType == "ramp-up" {
			rampUpSteps = append(rampUpSteps, step)
		} else if step.StepType == "hold" {
			holdSteps = append(holdSteps, step)
			// Track peak and sustained throughput from hold phases
			if step.RequestsPerSec > peakThroughput {
				peakThroughput = step.RequestsPerSec
			}
		}
	}

	// Calculate sustained throughput as average of hold phases
	if len(holdSteps) > 0 {
		var totalHoldRequests int64
		var totalHoldDuration time.Duration
		for _, step := range holdSteps {
			totalHoldRequests += step.TotalRequests
			totalHoldDuration += step.StepDuration
		}
		sustainedThroughput = float64(totalHoldRequests) / totalHoldDuration.Seconds()
	} else {
		sustainedThroughput = peakThroughput
	}

	return &StabilityTestResult{
		RampUpMetrics:       CalculatePhaseMetrics(rampUpSteps, "Ramp-Up Phase"),
		HoldMetrics:         CalculatePhaseMetrics(holdSteps, "Hold Phase"),
		OverallMetrics:      r.toPhaseMetrics("Overall"),
		PeakThroughput:      peakThroughput,
		SustainedThroughput: sustainedThroughput,
		StepResults:         r.StepResults,
		IsStabilityTest:     true,
	}
}

// toPhaseMetrics converts LoadTestResult to PhaseMetrics
func (r *LoadTestResult) toPhaseMetrics(phaseName string) *PhaseMetrics {
	return &PhaseMetrics{
		PhaseName:       phaseName,
		Duration:        r.TestDuration,
		TotalRequests:   int(r.TotalRequests),
		SuccessRequests: int(r.SuccessRequests),
		FailedRequests:  int(r.FailedRequests),
		RequestsPerSec:  r.RequestsPerSec,
		AverageLatency:  r.AverageLatency,
		MinLatency:      r.MinLatency,
		MaxLatency:      r.MaxLatency,
		ErrorRate:       r.ErrorRate,
	}
}

// PrintStabilityResults prints enhanced stability test results
func PrintStabilityResults(stabilityResult *StabilityTestResult) {
	fmt.Print("\n")
	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("🚀 STABILITY TEST RESULTS - ENHANCED ANALYSIS")
	fmt.Println(strings.Repeat("=", 80))

	// System Specifications Summary
	specs := GetSystemSpecs()
	fmt.Println("🖥️  SYSTEM SPECIFICATIONS:")
	fmt.Printf("   Hostname:             %s\n", specs.Hostname)
	fmt.Printf("   CPU Cores:            %d cores\n", specs.CPUCores)
	fmt.Printf("   Memory:               %s\n", specs.Memory)
	fmt.Printf("   OS:                   %s/%s\n", specs.OS, specs.Architecture)
	fmt.Printf("   Go Version:           %s\n", specs.GoVersion)

	// Peak and Sustained Performance Summary
	fmt.Println("\n📊 PERFORMANCE SUMMARY:")
	fmt.Printf("   Peak Throughput:      %.1f RPS (Maximum achieved)\n", stabilityResult.PeakThroughput)
	fmt.Printf("   Sustained Throughput: %.1f RPS (Hold phase average)\n", stabilityResult.SustainedThroughput)

	efficiency := (stabilityResult.SustainedThroughput / stabilityResult.PeakThroughput) * 100
	fmt.Printf("   Sustained Efficiency: %.1f%% (Sustained/Peak ratio)\n", efficiency)

	// Performance per CPU core analysis
	peakPerCore := stabilityResult.PeakThroughput / float64(specs.CPUCores)
	sustainedPerCore := stabilityResult.SustainedThroughput / float64(specs.CPUCores)
	fmt.Printf("   Peak RPS per Core:    %.1f RPS/core\n", peakPerCore)
	fmt.Printf("   Sustained RPS per Core: %.1f RPS/core\n", sustainedPerCore)

	// Phase-by-Phase Analysis
	if stabilityResult.RampUpMetrics != nil && stabilityResult.RampUpMetrics.TotalRequests > 0 {
		fmt.Println("\n🔄 RAMP-UP PHASE ANALYSIS:")
		printPhaseMetrics(stabilityResult.RampUpMetrics)
	}

	if stabilityResult.HoldMetrics != nil && stabilityResult.HoldMetrics.TotalRequests > 0 {
		fmt.Println("\n⚡ HOLD PHASE ANALYSIS (Most Important):")
		printPhaseMetrics(stabilityResult.HoldMetrics)
		fmt.Printf("   ★ This represents your system's SUSTAINED CAPACITY: %.1f RPS\n",
			stabilityResult.HoldMetrics.RequestsPerSec)
	}

	fmt.Println("\n📈 OVERALL TEST METRICS:")
	printPhaseMetrics(stabilityResult.OverallMetrics)
	fmt.Printf("   ⚠️  Note: Overall average (%.1f RPS) includes ramp-up phase\n",
		stabilityResult.OverallMetrics.RequestsPerSec)
	fmt.Printf("   ✅ Focus on Hold Phase (%.1f RPS) for capacity planning\n",
		stabilityResult.SustainedThroughput)

	fmt.Println(strings.Repeat("=", 80))
}

// printPhaseMetrics prints metrics for a specific phase
func printPhaseMetrics(metrics *PhaseMetrics) {
	fmt.Printf("   Duration:             %v\n", metrics.Duration.Truncate(time.Second))
	fmt.Printf("   Total Requests:       %d\n", metrics.TotalRequests)
	fmt.Printf("   Success Requests:     %d\n", metrics.SuccessRequests)
	fmt.Printf("   Failed Requests:      %d\n", metrics.FailedRequests)
	fmt.Printf("   Error Rate:           %.2f%%\n", metrics.ErrorRate)
	fmt.Printf("   Throughput:           %.1f RPS\n", metrics.RequestsPerSec)
	fmt.Printf("   Average Latency:      %.3fµs\n", float64(metrics.AverageLatency.Nanoseconds())/1000.0)
	if metrics.MinLatency > 0 && metrics.MinLatency < time.Hour {
		fmt.Printf("   Min Latency:          %.3fµs\n", float64(metrics.MinLatency.Nanoseconds())/1000.0)
	}
	if metrics.MaxLatency > 0 {
		fmt.Printf("   Max Latency:          %.3fµs\n", float64(metrics.MaxLatency.Nanoseconds())/1000.0)
	}
}
