package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gartnera/actions-runner-ephemeral-autoscaler/autoscaler"
	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/gcp"
	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/githubtoken"
	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/interfaces"
	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/lxd"
	"github.com/google/go-github/v68/github"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/oauth2"
)

func main() {
	org := flag.String("org", os.Getenv("GITHUB_ORG"), "GitHub organization name")
	repo := flag.String("repo", os.Getenv("GITHUB_REPO"), "GitHub repository name")
	labels := flag.String("labels", "", "Runner labels")
	targetIdle := flag.Int("target-idle", 1, "Target number of idle runners")
	customCloudInitPath := flag.String("custom-cloud-init", "", "Path to custom cloud init file")
	providerName := flag.String("provider", "lxd", "Provider to use (only 'lxd' supported)")
	flag.Parse()

	if *org == "" || *repo == "" || *labels == "" {
		flag.Usage()
		os.Exit(1)
	}

	var provider interfaces.Provider
	var err error

	ctx := context.Background()
	switch *providerName {
	case "lxd":
		provider, err = lxd.New()
	case "gcp":
		provider, err = gcp.New()
	default:
		fmt.Printf("Invalid provider %s, options are lxd|gcp", *providerName)
		os.Exit(2)
	}
	if err != nil {
		panic(err)
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")})
	tc := oauth2.NewClient(ctx, ts)
	tokenProvider := &githubtoken.RepoProvider{
		Client: github.NewClient(tc),
		Org:    *org,
		Repo:   *repo,
	}

	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(":9090", nil)

	prepareOpts := interfaces.PrepareOptions{}
	if *customCloudInitPath != "" {
		customCloudInitBytes, err := os.ReadFile(*customCloudInitPath)
		if err != nil {
			panic(fmt.Errorf("reading %s: %w", *customCloudInitPath, err))
		}
		prepareOpts.CustomCloudInitOverlay = string(customCloudInitBytes)
	}

	autoscaler := autoscaler.New(provider, tokenProvider, autoscaler.AutoscalerConfig{
		TargetIdle:     *targetIdle,
		Labels:         *labels,
		PrepareOptions: prepareOpts,
	})

	// only clear resources on SIGINT
	sigIntChan := make(chan os.Signal, 1)
	signal.Notify(sigIntChan, syscall.SIGINT, syscall.SIGTERM)
	ctx, cancel := context.WithCancel(ctx)
	go func() {
		sig := <-sigIntChan
		if sig == syscall.SIGTERM {
			os.Exit(0)
		}
		fmt.Printf("Received signal %v, clearing existing resources...\n", sig)
		cancel()
	}()

	ticker := time.NewTicker(time.Second * 2)

	for i := 0; ; i++ {
		// check prepare every 500 iterations (including first)
		shouldCheckPrepare := i%500 == 0
		err := autoscaler.Autoscale(ctx, shouldCheckPrepare)
		if err != nil {
			if ctx.Err() != nil {
				if ctx.Err() == nil {
					fmt.Printf("autoscale failed: %v\n", err)
				}
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			ctx := context.Background()
			err := autoscaler.Cleanup(ctx)
			if err != nil {
				fmt.Printf("cleanup failed: %v\n", err)
			}
			return
		}
	}
}
