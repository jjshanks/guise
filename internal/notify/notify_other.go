//go:build !windows

package notify

import "log"

// Error logs the message off Windows so the pure logic stays buildable and
// testable anywhere.
func Error(title, message string) {
	log.Printf("notify error: %s: %s", title, message)
}

// Info logs an informational message off Windows.
func Info(title, message string) {
	log.Printf("notify info: %s: %s", title, message)
}
