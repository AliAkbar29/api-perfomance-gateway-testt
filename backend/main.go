package main

import (
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	// Default payload size in KB
	DEFAULT_PAYLOAD_SIZE_KB = 1
	// Characters for random string generation
	ALPHANUMERIC_CHARS = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// generateRandomPayload generates a random alphanumeric string of specified size in KB
func generateRandomPayload(sizeKB int) string {
	// Calculate total characters needed (1 KB = 1024 bytes = 1024 characters for ASCII)
	totalChars := sizeKB * 1024

	// Create string builder for efficiency
	var builder strings.Builder
	builder.Grow(totalChars)

	// Generate random characters
	for i := 0; i < totalChars; i++ {
		randomIndex := rand.Intn(len(ALPHANUMERIC_CHARS))
		builder.WriteByte(ALPHANUMERIC_CHARS[randomIndex])
	}

	return builder.String()
}

func helloHandler(w http.ResponseWriter, r *http.Request) {
	now := time.Now().Format(time.RFC3339Nano)

	// Get payload size from query parameter, default to 1KB
	payloadSizeKB := DEFAULT_PAYLOAD_SIZE_KB
	if sizeParam := r.URL.Query().Get("size"); sizeParam != "" {
		if size, err := strconv.Atoi(sizeParam); err == nil && size > 0 {
			payloadSizeKB = size
		}
	}

	// Generate random payload
	payload := generateRandomPayload(payloadSizeKB)

	// Set content type
	w.Header().Set("Content-Type", "text/plain")

	// Send response with timestamp and payload
	fmt.Fprintf(w, "Hello from backend! Request at %s\nPayload size: %d KB\nPayload: %s\n",
		now, payloadSizeKB, payload)
}

func main() {
	// Seed random number generator
	rand.Seed(time.Now().UnixNano())

	http.HandleFunc("/hello", helloHandler)
	log.Printf("Backend running on :9000")
	log.Printf("Default payload size: %d KB", DEFAULT_PAYLOAD_SIZE_KB)
	log.Println("Use ?size=X query parameter to specify payload size in KB")
	log.Fatal(http.ListenAndServe(":9000", nil))
}