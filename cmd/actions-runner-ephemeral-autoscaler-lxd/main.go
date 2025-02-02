package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/gartnera/actions-runner-ephemeral-autoscaler/autoscaler"
	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/lxd"
	"github.com/google/go-github/v68/github"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/oauth2"
)

type ActionsRepoTokenProvider struct {
	Client *github.Client
	Org    string
	Repo   string
}

func (p *ActionsRepoTokenProvider) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s", p.Org, p.Repo)
}

func (p *ActionsRepoTokenProvider) Token(ctx context.Context) (string, error) {
	tokenResponse, _, err := p.Client.Actions.CreateRegistrationToken(ctx, p.Org, p.Repo)
	if err != nil {
		return "", fmt.Errorf("creating registration token: %v", err)
	}
	return tokenResponse.GetToken(), nil
}

func main() {
	org := flag.String("org", os.Getenv("GITHUB_ORG"), "GitHub organization name")
	repo := flag.String("repo", os.Getenv("GITHUB_REPO"), "GitHub repository name")
	labels := flag.String("labels", "", "Runner labels")
	targetIdle := flag.Int("target-idle", 1, "Target number of idle runners")
	flag.Parse()

	if *org == "" || *repo == "" || *labels == "" {
		flag.Usage()
		os.Exit(1)
	}

	ctx := context.Background()
	provider, err := lxd.New()
	if err != nil {
		panic(err)
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")})
	tc := oauth2.NewClient(ctx, ts)
	tokenProvider := &ActionsRepoTokenProvider{
		Client: github.NewClient(tc),
		Org:    *org,
		Repo:   *repo,
	}

	http.Handle("/metrics", promhttp.Handler())
	go http.ListenAndServe(":9090", nil)

	autoscaler := autoscaler.New(provider, tokenProvider, autoscaler.AutoscalerConfig{
		TargetIdle: *targetIdle,
		Labels:     *labels,
	})

	for i := 0; ; i++ {
		// check prepare every 500 iterations (including first)
		shouldCheckPrepare := i%500 == 0
		err := autoscaler.Autoscale(ctx, shouldCheckPrepare)
		if err != nil {
			fmt.Printf("autoscale failed: %v\n", err)
		}
		time.Sleep(time.Second * 2)
	}
}
