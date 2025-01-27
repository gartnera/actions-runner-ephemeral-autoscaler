package interfaces

import "context"

// RunnerDispositionMetrics represents the metrics of runner instances in different states
type RunnerDispositionMetrics interface {
	// TotalCount returns the total number of runner instances
	TotalCount() int

	// StartingCount returns the number of runner instances in starting state
	StartingCount() int

	// IdleCount returns the number of runner instances in idle state
	IdleCount() int

	// ActiveCount returns the number of runner instances in active state
	ActiveCount() int
}

// Provider represents a compute provider interface
type Provider interface {
	// PrepareImage preheats an image with required packages
	PrepareImage(ctx context.Context) error

	// CreateRunner creates a new runner instance
	CreateRunner(ctx context.Context, url, token string) error

	// RunnerDisposition returns the current state of runners
	RunnerDisposition(ctx context.Context) (RunnerDispositionMetrics, error)
}
