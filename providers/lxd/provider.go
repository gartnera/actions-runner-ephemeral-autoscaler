package lxd

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	lxdClient "github.com/canonical/lxd/client"
	"github.com/canonical/lxd/shared/api"
	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/common"
	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/interfaces"
	"github.com/samber/lo"
)

const actionsRunnerEphemeralKey = "user.actions-runner-ephemeral"
const imageAliasName = "actions-runner-ephemeral"

type Provider struct {
	client lxdClient.InstanceServer
}

func New() (*Provider, error) {
	lxd, err := lxdClient.ConnectLXDUnix("", nil)
	if err != nil {
		return nil, err
	}
	return &Provider{
		client: lxd,
	}, nil
}

// ImageCreatedAt gets the creation timestamp of the latest image.
// This is typically used to determine if we need to call PrepareImage.
func (p *Provider) ImageCreatedAt(ctx context.Context) (time.Time, error) {
	alias, _, err := p.client.GetImageAlias(imageAliasName)
	if err != nil {
		if api.StatusErrorCheck(err, http.StatusNotFound) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("get image alias: %w", err)
	}
	image, _, err := p.client.GetImage(alias.Target)
	if err != nil {
		return time.Time{}, fmt.Errorf("get image: %w", err)
	}
	return image.CreatedAt, nil
}

// PrepareImage preheats an image so that all required packages are installed
func (p *Provider) PrepareImage(ctx context.Context, opts interfaces.PrepareOptions) error {
	id := fmt.Sprintf("%s-prepare", imageAliasName)
	cloudInitPrepare, err := common.GetCloudInitPrepare(ctx, opts.CustomCloudInitOverlay)
	if err != nil {
		return fmt.Errorf("get cloud init prepare: %w", err)
	}
	createOp, err := p.client.CreateInstance(api.InstancesPost{
		Name: id,
		Source: api.InstanceSource{
			Type:     "image",
			Protocol: "simplestreams",
			Server:   "https://cloud-images.ubuntu.com/releases",
			Alias:    "jammy",
		},
		InstancePut: api.InstancePut{
			Config: map[string]string{
				"security.nesting": "true",
				"user.vendor-data": cloudInitPrepare,
			},
			Profiles: []string{"default"},
		},
		Type: api.InstanceTypeContainer,
	})
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}
	err = createOp.Wait()
	if err != nil {
		return fmt.Errorf("waiting for container creation: %w", err)
	}

	startOp, err := p.client.UpdateInstanceState(id, api.InstanceStatePut{Action: "start"}, "")
	if err != nil {
		return fmt.Errorf("starting container: %w", err)
	}
	err = startOp.Wait()
	if err != nil {
		return fmt.Errorf("waiting for container start: %w", err)
	}

	// Wait for the container to shutdown
	for {
		instance, _, err := p.client.GetInstance(id)
		if err != nil {
			return fmt.Errorf("getting container status: %w", err)
		}
		if instance.StatusCode == api.Stopped {
			break
		}
		// Add a small delay to avoid hammering the API
		time.Sleep(1 * time.Second)
	}

	// Create a new image from the container
	imageCreateOp, err := p.client.CreateImage(api.ImagesPost{
		Source: &api.ImagesPostSource{
			Type: "container",
			Name: id,
		},
		ImagePut: api.ImagePut{
			ExpiresAt: time.Now().Add(time.Hour * 24 * 7),
		},
	}, nil)
	if err != nil {
		return fmt.Errorf("creating image: %w", err)
	}
	err = imageCreateOp.Wait()
	if err != nil {
		return fmt.Errorf("waiting for image creation: %w", err)
	}

	// no need to wait for deletion
	deleteOp, err := p.client.DeleteInstance(id)
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}
	deleteOp.Get()

	// Create the alias
	// we do this after so that the update is as atomic as possible
	fingerprint := imageCreateOp.Get().Metadata["fingerprint"].(string)

	// Try to get the existing alias first
	alias, etag, err := p.client.GetImageAlias(imageAliasName)
	if err == nil {
		// Alias exists, update it
		err = p.client.UpdateImageAlias(alias.Name, api.ImageAliasesEntryPut{
			Target:      fingerprint,
			Description: "Pre-prepared Actions Runner image",
		}, etag)
	} else {
		// Alias doesn't exist, create it
		err = p.client.CreateImageAlias(api.ImageAliasesPost{
			ImageAliasesEntry: api.ImageAliasesEntry{
				Name:        imageAliasName,
				Target:      fingerprint,
				Description: "Pre-prepared Actions Runner image",
			},
		})
	}
	if err != nil {
		return fmt.Errorf("create/update image alias: %w", err)
	}

	return nil
}

func (p *Provider) CreateRunner(ctx context.Context, url, token, labels string) error {
	id := fmt.Sprintf("actions-runner-ephemeral-%s", lo.RandomString(5, lo.LettersCharset))
	cloudInitConf := common.GetCloudInitStart(url, token, labels)
	createOp, err := p.client.CreateInstance(api.InstancesPost{
		Name: id,
		Source: api.InstanceSource{
			Type:  "image",
			Alias: imageAliasName,
		},
		InstancePut: api.InstancePut{
			Config: map[string]string{
				actionsRunnerEphemeralKey: "true",
				"security.nesting":        "true",
				"user.vendor-data":        cloudInitConf,
			},
			Profiles:  []string{"default"},
			Ephemeral: true,
		},
		Type: api.InstanceTypeContainer,
	})
	if err != nil {
		return fmt.Errorf("creating container: %w", err)
	}
	err = createOp.Wait()
	if err != nil {
		return fmt.Errorf("waiting for container creation: %w", err)
	}
	startOp, err := p.client.UpdateInstanceState(id, api.InstanceStatePut{Action: "start"}, "")
	if err != nil {
		return fmt.Errorf("starting container: %w", err)
	}
	err = startOp.Wait()
	if err != nil {
		return fmt.Errorf("waiting for container start: %w", err)
	}
	return nil
}

// DeleteRunners deletes N runner instances. If wait is true, it waits for the deletion to complete.
//
// All we have to do is stop the runner since the instances are ephemeral
func (p *Provider) DeleteRunners(ctx context.Context, count int, wait bool) error {
	instances, err := p.client.GetInstancesWithFilter(api.InstanceTypeContainer, []string{fmt.Sprintf("config.%s=true", actionsRunnerEphemeralKey)})
	if err != nil {
		return fmt.Errorf("getting instances: %w", err)
	}

	// Start stop operations for up to count instances
	stopOps := make([]lxdClient.Operation, 0, count)
	stopNames := make([]string, 0, count)
	for i := 0; i < count && i < len(instances); i++ {
		// Stop the instance
		stopOp, err := p.client.UpdateInstanceState(instances[i].Name, api.InstanceStatePut{Action: "stop"}, "")
		if err != nil {
			return fmt.Errorf("stop instance %s: %w", instances[i].Name, err)
		}
		stopOps = append(stopOps, stopOp)
		stopNames = append(stopNames, instances[i].Name)
	}

	// Wait for all stop operations to complete
	if wait {
		for i, op := range stopOps {
			op.Get()
			err = op.Wait()
			if err != nil {
				return fmt.Errorf("waiting for instance stop %s: %w", stopNames[i], err)
			}
		}
	}

	return nil
}

type disposition struct {
	startingCount int
	idleCount     int
	activeCount   int
}

func (d disposition) TotalCount() int {
	return d.activeCount + d.idleCount + d.startingCount
}
func (d disposition) StartingCount() int {
	return d.startingCount
}
func (d disposition) IdleCount() int {
	return d.idleCount
}
func (d disposition) ActiveCount() int {
	return d.activeCount
}

func (p *Provider) RunnerDisposition(ctx context.Context) (interfaces.RunnerDispositionMetrics, error) {
	instances, err := p.client.GetInstancesWithFilter(api.InstanceTypeContainer, []string{fmt.Sprintf("config.%s=true", actionsRunnerEphemeralKey)})
	if err != nil {
		return disposition{}, fmt.Errorf("getting instances: %w", err)
	}
	res := disposition{}
	for _, instance := range instances {
		contentReader, _, err := p.client.GetInstanceFile(instance.Name, "/tmp/actions-runner-state")
		if err != nil {
			res.startingCount++
			continue
		}
		defer contentReader.Close()
		contentBytes, err := io.ReadAll(contentReader)
		if err != nil {
			fmt.Printf("error reading content from %s: %v\n", instance.Name, err)
			continue
		}
		content := strings.TrimSpace(string(contentBytes))
		switch content {
		case "active":
			res.activeCount++
		case "idle":
			res.idleCount++
		default:
			res.startingCount++
		}
	}
	return res, nil
}
