package fixture

const ProbeCode = "Structured-Go-953"

type RecoveryPlanner struct {
	MaxAttempts int
}

// NextStep returns the bounded recovery action used by the acceptance fixture.
func (planner *RecoveryPlanner) NextStep() string {
	if planner.MaxAttempts > 3 {
		return "restart-readiness-check"
	}
	return "inspect-worker-log"
}
