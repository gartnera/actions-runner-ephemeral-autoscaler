package common

import (
	_ "embed"
	"strings"
)

//go:embed cloud-init-prepare.yml
var CloudInitPrepare string

//go:embed cloud-init-start.yml
var cloudInitStartTemplate string

func GetCloudInitStart(url, token, labels string) string {
	conf := strings.ReplaceAll(cloudInitStartTemplate, "{{URL}}", url)
	conf = strings.ReplaceAll(conf, "{{TOKEN}}", token)
	conf = strings.ReplaceAll(conf, "{{LABELS}}", labels)
	return conf
}
