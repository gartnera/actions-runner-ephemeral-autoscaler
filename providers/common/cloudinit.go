package common

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/google/go-github/v68/github"
	"gopkg.in/yaml.v3"
)

//go:embed cloud-init-prepare.yml
var cloudInitPrepare string

func GetCloudInitPrepare(ctx context.Context, customInitOverlay string) (string, error) {
	client := github.NewClient(nil)
	release, _, err := client.Repositories.GetLatestRelease(ctx, "actions", "runner")
	if err != nil {
		return "", err
	}
	runnerVersion := strings.TrimPrefix(release.GetTagName(), "v")
	baseConfStr := strings.ReplaceAll(cloudInitPrepare, "{{RUNNER_VERSION}}", runnerVersion)

	var baseNode, overlayNode yaml.Node
	err = yaml.Unmarshal([]byte(baseConfStr), &baseNode)
	if err != nil {
		return "", fmt.Errorf("decoding base config: %w", err)
	}
	err = yaml.Unmarshal([]byte(customInitOverlay), &overlayNode)
	if err != nil {
		return "", fmt.Errorf("decoding custom overlay: %w", err)
	}

	mergeNodes(baseNode.Content[0], overlayNode.Content[0])

	finalConf, err := yaml.Marshal(&baseNode)
	if err != nil {
		return "", fmt.Errorf("marshaling final conf: %w", err)
	}

	return string(finalConf), nil
}

//go:embed cloud-init-start.yml
var cloudInitStartTemplate string

func GetCloudInitStart(url, token, labels string) string {
	conf := strings.ReplaceAll(cloudInitStartTemplate, "{{URL}}", url)
	conf = strings.ReplaceAll(conf, "{{TOKEN}}", token)
	conf = strings.ReplaceAll(conf, "{{LABELS}}", labels)
	return conf
}

// mergeNodes merges overlay into base. It merges maps and sequences which
// is quite different from most behavior your may expect.
func mergeNodes(base, overlay *yaml.Node) {
	switch base.Kind {
	case yaml.MappingNode:
		for i := 0; i < len(overlay.Content); i += 2 {
			oKey, oVal := overlay.Content[i], overlay.Content[i+1]
			found := false
			// Look for matching key in base.
			for j := 0; j < len(base.Content); j += 2 {
				bKey, bVal := base.Content[j], base.Content[j+1]
				if bKey.Value == oKey.Value {
					mergeNodes(bVal, oVal)
					found = true
					break
				}
			}
			if !found {
				base.Content = append(base.Content, oKey, oVal)
			}
		}
	case yaml.SequenceNode:
		// Append each item from override (discarding its comments)
		for _, item := range overlay.Content {
			base.Content = append(base.Content, item.Content...)
		}
	default:
		// For scalars, simply override the value and tag,
		// but leave base's comments intact.
		base.Value = overlay.Value
		base.Tag = overlay.Tag
	}
}
