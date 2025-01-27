package autoscaler

import (
	"context"
	"fmt"
	"log"

	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/interfaces"
)

type AutoscalerConfig struct {
	TargetIdle int
}

type RunnerTokenProvider interface {
	URL() string
	Token(context.Context) (string, error)
}

type Autoscaler struct {
	provider      interfaces.Provider
	tokenProvider RunnerTokenProvider
	config        AutoscalerConfig
}

func New(provider interfaces.Provider, tokenProvider RunnerTokenProvider, config AutoscalerConfig) *Autoscaler {
	return &Autoscaler{
		provider:      provider,
		config:        config,
		tokenProvider: tokenProvider,
	}
}

func (a *Autoscaler) Autoscale(ctx context.Context) error {
	metrics, err := a.provider.RunnerDisposition(ctx)
	if err != nil {
		return fmt.Errorf("get runner disposition")
	}
	log.Printf("status -> starting: %d, idle: %d, active: %d, total: %d", metrics.StartingCount(), metrics.IdleCount(), metrics.ActiveCount(), metrics.TotalCount())
	idleStartingCount := metrics.StartingCount() + metrics.IdleCount()
	for i := idleStartingCount; i < a.config.TargetIdle; i++ {
		log.Printf("creating instance (%d < %d)", i, a.config.TargetIdle)
		url := a.tokenProvider.URL()
		token, err := a.tokenProvider.Token(ctx)
		if err != nil {
			return fmt.Errorf("get runner token: %w", err)
		}
		err = a.provider.CreateRunner(ctx, url, token)
		if err != nil {
			return fmt.Errorf("create runner: %w", err)
		}
		log.Println("instance created")
	}
	return nil
}
