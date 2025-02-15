package githubtoken

import (
	"context"
	"fmt"

	"github.com/google/go-github/v68/github"
)

type RepoProvider struct {
	Client *github.Client
	Org    string
	Repo   string
}

func (p *RepoProvider) URL() string {
	return fmt.Sprintf("https://github.com/%s/%s", p.Org, p.Repo)
}

func (p *RepoProvider) Token(ctx context.Context) (string, error) {
	tokenResponse, _, err := p.Client.Actions.CreateRegistrationToken(ctx, p.Org, p.Repo)
	if err != nil {
		return "", fmt.Errorf("creating registration token: %v", err)
	}
	return tokenResponse.GetToken(), nil
}
