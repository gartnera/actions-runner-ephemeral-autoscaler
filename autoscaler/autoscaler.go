package autoscaler

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/interfaces"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

const (
	metricsNamespace = "actions_runner_autoscaler"
	maximumImageAge  = time.Hour * 24
)

var (
	totalRunners = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "total",
		Help:      "Total number of GitHub Actions runners",
	})
	startingRunners = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "starting",
		Help:      "Number of GitHub Actions runners in starting state",
	})
	idleRunners = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "idle",
		Help:      "Number of GitHub Actions runners in idle state",
	})
	activeRunners = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "active",
		Help:      "Number of GitHub Actions runners in active state",
	})
	preparingRunners = promauto.NewGauge(prometheus.GaugeOpts{
		Namespace: metricsNamespace,
		Name:      "preparing",
		Help:      "Number of instances running for image preparation",
	})
)

type AutoscalerConfig struct {
	TargetIdle int
	Labels     string
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

func updateMetrics(metrics interfaces.RunnerDispositionMetrics) {
	totalRunners.Set(float64(metrics.TotalCount()))
	startingRunners.Set(float64(metrics.StartingCount()))
	idleRunners.Set(float64(metrics.IdleCount()))
	activeRunners.Set(float64(metrics.ActiveCount()))
}

func (a *Autoscaler) maybePrepare(ctx context.Context) error {
	createdAt, err := a.provider.ImageCreatedAt(ctx)
	if err != nil {
		return fmt.Errorf("get image created at: %w", err)
	}
	createdLag := time.Now().Sub(createdAt)
	if createdLag < maximumImageAge {
		return nil
	}
	preparingRunners.Inc()
	defer preparingRunners.Dec()
	err = a.provider.PrepareImage(ctx)
	if err != nil {
		return fmt.Errorf("prepare image: %w", err)
	}
	return nil
}

func (a *Autoscaler) Autoscale(ctx context.Context, checkPrepare bool) error {
	if checkPrepare {
		err := a.maybePrepare(ctx)
		if err == nil {
			log.Default().Printf("error when preparing: %v", err)
		}
	}
	metrics, err := a.provider.RunnerDisposition(ctx)
	if err != nil {
		return fmt.Errorf("get runner disposition")
	}

	updateMetrics(metrics)

	log.Printf("status -> starting: %d, idle: %d, active: %d, total: %d", metrics.StartingCount(), metrics.IdleCount(), metrics.ActiveCount(), metrics.TotalCount())
	idleStartingCount := metrics.StartingCount() + metrics.IdleCount()

	for i := idleStartingCount; i < a.config.TargetIdle; i++ {
		log.Printf("creating instance (%d < %d)", i, a.config.TargetIdle)
		url := a.tokenProvider.URL()
		token, err := a.tokenProvider.Token(ctx)
		if err != nil {
			return fmt.Errorf("get runner token: %w", err)
		}
		err = a.provider.CreateRunner(ctx, url, token, a.config.Labels)
		if err != nil {
			return fmt.Errorf("create runner: %w", err)
		}
		log.Println("instance created")
	}
	return nil
}
