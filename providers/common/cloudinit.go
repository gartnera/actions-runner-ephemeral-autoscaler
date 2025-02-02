package common

import (
	"context"
	_ "embed"
	"strings"

	"github.com/google/go-github/v68/github"
)

//go:embed cloud-init-prepare.yml
var cloudInitPrepare string

func GetCloudInitPrepare(ctx context.Context) (string, error) {
	client := github.NewClient(nil)
	release, _, err := client.Repositories.GetLatestRelease(ctx, "actions", "runner")
	if err != nil {
		return "", err
	}
	runnerVersion := strings.TrimPrefix(release.GetTagName(), "v")
	conf := strings.ReplaceAll(cloudInitPrepare, "{{RUNNER_VERSION}}", runnerVersion)
	return conf, nil
}

//go:embed cloud-init-start.yml
var cloudInitStartTemplate string

func GetCloudInitStart(url, token, labels string) string {
	conf := strings.ReplaceAll(cloudInitStartTemplate, "{{URL}}", url)
	conf = strings.ReplaceAll(conf, "{{TOKEN}}", token)
	conf = strings.ReplaceAll(conf, "{{LABELS}}", labels)
	return conf
}
