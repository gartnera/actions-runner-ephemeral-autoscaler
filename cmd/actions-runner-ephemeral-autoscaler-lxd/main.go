package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/gartnera/actions-runner-ephemeral-autoscaler/autoscaler"
	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/lxd"
	"github.com/google/go-github/v68/github"
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
	ctx := context.Background()
	provider, err := lxd.New()
	if err != nil {
		panic(err)
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: os.Getenv("GITHUB_TOKEN")})
	tc := oauth2.NewClient(ctx, ts)
	tokenProvider := &ActionsRepoTokenProvider{
		Client: github.NewClient(tc),
		Org:    os.Args[1],
		Repo:   os.Args[2],
	}

	if _, ok := os.LookupEnv("DO_PREPARE"); ok {
		err = provider.PrepareImage(ctx)
		if err != nil {
			panic(err)
		}
	}

	autoscaler := autoscaler.New(provider, tokenProvider, autoscaler.AutoscalerConfig{
		TargetIdle: 1,
	})

	for {
		err := autoscaler.Autoscale(ctx)
		if err != nil {
			fmt.Printf("autoscale failed: %v\n", err)
		}
		time.Sleep(time.Second * 2)
	}

}
