package scan_test

import (
	"fmt"
	"time"

	"goft/internal/scan"
)

// A file is only handed on once it has stopped changing, which is what keeps a
// half-written file from being transferred.
func ExampleStabilizer() {
	now := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	s := scan.NewStabilizer(3*time.Second, func() time.Time { return now })

	// First sight of a file says nothing about whether it is finished.
	growing := []scan.File{{Path: "report.csv", Size: 1024, ModTime: now}}
	fmt.Println(len(s.Observe(growing)))

	// Two seconds on it has grown, so the wait starts over.
	now = now.Add(2 * time.Second)
	grown := []scan.File{{Path: "report.csv", Size: 4096, ModTime: now}}
	fmt.Println(len(s.Observe(grown)))

	// Four seconds after that last change nothing has moved, so it is ready.
	now = now.Add(4 * time.Second)
	ready := s.Observe(grown)
	fmt.Println(len(ready), ready[0].Path)

	// Output:
	// 0
	// 0
	// 1 report.csv
}
