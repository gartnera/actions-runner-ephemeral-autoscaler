package common

import (
	_ "embed"
	"strings"
)

//go:embed cloud-init-prepare.yml
var CloudInitPrepare string

//go:embed cloud-init-start.yml
var cloudInitStartTemplate string

func GetCloudInitStart(url, token string) string {
	conf := strings.ReplaceAll(cloudInitStartTemplate, "{{URL}}", url)
	conf = strings.ReplaceAll(conf, "{{TOKEN}}", token)
	return conf
}
