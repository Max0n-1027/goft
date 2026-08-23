package config_test

import (
	"fmt"
	"time"

	"goft/internal/config"
)

// The wait between attempts grows, so a server that needs a moment to come back
// is given one without the run stalling for long on a failure that will not
// clear.
func ExampleRetry_Wait() {
	r := config.Retry{MaxAttempts: 4, Interval: 2 * time.Second, Backoff: 2}

	for attempt := 1; attempt < r.MaxAttempts; attempt++ {
		fmt.Printf("retry %d after %v\n", attempt, r.Wait(attempt))
	}
	// Output:
	// retry 1 after 2s
	// retry 2 after 4s
	// retry 3 after 8s
}

// Naming fields in the configuration decides exactly what a transfer record
// carries; the level only decides which records get written at all.
func ExampleConfig_LogFields() {
	var cfg config.Config
	cfg.Log.Fields = []string{config.FieldSrc, config.FieldResult, config.FieldHashDst}

	set := cfg.LogFields(0)
	for _, field := range config.SelectableLogFields {
		if set.Has(field) {
			fmt.Println(field)
		}
	}
	// Output:
	// src
	// result
	// hash_dst
}
