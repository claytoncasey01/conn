// Package testtime stretches the time limits tests wait for, so a slow machine (a busy CI runner,
// the race detector) can give every test more time at once instead of each test growing its own
// margin.
package testtime

import (
	"os"
	"strconv"
	"time"
)

// scaleVariable names the factor: CONN_TEST_TIME_SCALE=3 makes every limit three times longer.
const scaleVariable = "CONN_TEST_TIME_SCALE"

// Scaled is limit stretched by CONN_TEST_TIME_SCALE, or limit when that is unset or not a number
// above 1.
func Scaled(limit time.Duration) time.Duration {
	value, ok := os.LookupEnv(scaleVariable)
	if !ok {
		return limit
	}

	factor, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return limit
	}

	if factor <= 1 {
		return limit
	}

	return time.Duration(float64(limit) * factor)
}
