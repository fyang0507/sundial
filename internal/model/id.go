package model

import (
	"crypto/rand"
	"fmt"
)

// NewScheduleID generates a new schedule ID in the format "sch_" + 6 hex chars.
// e.g., "sch_a1b2c3"
func NewScheduleID() string {
	b := make([]byte, 3) // 3 bytes = 6 hex chars
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("failed to generate schedule ID: %v", err))
	}
	return fmt.Sprintf("sch_%x", b)
}
