package interfaces

import (
	"context"
	"time"
)

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
	ImageCreatedAt(ctx context.Context) (time.Time, error)
	// PrepareImage preheats an image with required packages
	PrepareImage(ctx context.Context, opts PrepareOptions) error

	// CreateRunner creates a new runner instance
	CreateRunner(ctx context.Context, url, token, labels string) error

	// RunnerDisposition returns the current state of runners
	RunnerDisposition(ctx context.Context) (RunnerDispositionMetrics, error)
}

type PrepareOptions struct {
	CustomCloudInitOverlay string
}
