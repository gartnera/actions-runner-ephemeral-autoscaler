# ephemeral-actions-runner-autoscaler

There are a lot of github actions self hosted runner autoscalers. But they are focused on a single cloud compute provider or kubernetes. This one tries to keep the compute providers abstracted so that you can easily make it work with any cloud provider.

It's intended to be flexible enough to run as a systemd service, a docker container on kubernetes, or serverless provider. It can scale to zero and also keep warm instances.

It currently only supports LXD while in development. See issues for planned work.

## Usage

Create a github PAT with administration scope on your repository. Set `DO_PREPARE=true` on the first run to create the correct image.

```
go install github.com/gartnera/actions-runner-ephemeral-autoscaler/cmd/actions-runner-ephemeral-autoscaler-lxd@latest

GITHUB_TOKEN=mytoken actions-runner-ephemeral-autoscaler-lxd -org <github user> -repo <github repo> -labels <comma separated labels>
```

You will eventually see autoscaler status information printed stdout:

```
2025/01/26 17:54:59 status -> starting: 0, idle: 1, active: 0, total: 1
2025/01/26 17:55:01 status -> starting: 0, idle: 0, active: 1, total: 1
2025/01/26 17:55:01 creating instance (0 < 1)
2025/01/26 17:55:11 instance created
2025/01/26 17:55:13 status -> starting: 1, idle: 0, active: 0, total: 1
2025/01/26 17:55:15 status -> starting: 1, idle: 0, active: 0, total: 1
2025/01/26 17:55:17 status -> starting: 1, idle: 0, active: 0, total: 1
2025/01/26 17:55:19 status -> starting: 1, idle: 0, active: 0, total: 1
2025/01/26 17:55:21 status -> starting: 0, idle: 1, active: 0, total: 1
2025/01/26 17:55:23 status -> starting: 0, idle: 1, active: 0, total: 1
```