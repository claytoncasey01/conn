//go:build !race

package costtest_test

// raceSlowdown is how much slower the race detector makes the screen's work: none without it.
const raceSlowdown = 1
