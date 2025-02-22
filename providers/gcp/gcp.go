package gcp

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	_ "embed"

	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/common"
	"github.com/gartnera/actions-runner-ephemeral-autoscaler/providers/interfaces"
	compute "google.golang.org/api/compute/v1"

	"github.com/samber/lo"
)

const labelStatusPreparing = "preparing"
const labelStatusStarting = "starting"

// must be lowercase because of gcp api requirements
const typeLabelValue = "actions-runner-ephemeral"

var typeLabelFilter = fmt.Sprintf("labels.type=%s", typeLabelValue)

//go:embed cloud-init-prepare.yml
var cloudInitPrepareOverlay string

type Provider struct {
	client    *compute.Service
	projectID string
	zone      string
	template  string
}

func getRegionFromZone(zone string) string {
	parts := strings.Split(zone, "-")
	return strings.Join(parts[:len(parts)-1], "-")
}

func mustGetEnv(key string) (string, error) {
	res, ok := os.LookupEnv(key)
	if !ok {
		return "", fmt.Errorf("%s must be set", key)
	}
	return res, nil
}

func New() (*Provider, error) {
	ctx := context.Background()
	client, err := compute.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create compute client: %w", err)
	}

	project, err := mustGetEnv("GOOGLE_CLOUD_PROJECT")
	if err != nil {
		return nil, err
	}

	zone, err := mustGetEnv("GOOGLE_CLOUD_ZONE")
	if err != nil {
		return nil, err
	}

	template := os.Getenv("GOOGLE_CLOUD_INSTANCE_TEMPLATE")

	return &Provider{
		client:    client,
		projectID: project,
		zone:      zone,
		template:  template,
	}, nil
}

func (p *Provider) getLatestImage(ctx context.Context) (*compute.Image, error) {
	resp, err := p.client.Images.List(p.projectID).Filter(typeLabelFilter).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}

	if len(resp.Items) == 0 {
		return nil, nil
	}

	// Sort images by creation timestamp in descending order
	images := resp.Items
	sort.Slice(images, func(i, j int) bool {
		return images[i].CreationTimestamp > images[j].CreationTimestamp
	})
	return images[0], nil
}

func (p *Provider) ImageCreatedAt(ctx context.Context) (time.Time, error) {
	image, err := p.getLatestImage(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("get latest image: %w", err)
	}
	if image == nil {
		return time.Time{}, nil
	}

	return time.Parse(time.RFC3339, image.CreationTimestamp)
}

func (p *Provider) PrepareImage(ctx context.Context, opts interfaces.PrepareOptions) error {
	instanceName := fmt.Sprintf("%s-prepare", typeLabelValue)
	cloudInitPrepare, err := common.GetCloudInitPrepare(ctx, cloudInitPrepareOverlay, opts.CustomCloudInitOverlay)
	if err != nil {
		return fmt.Errorf("get cloud init prepare: %w", err)
	}

	instance := &compute.Instance{
		Name: instanceName,
		Labels: map[string]string{
			"type":   typeLabelValue,
			"status": labelStatusPreparing,
		},
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					Key:   "user-data",
					Value: &cloudInitPrepare,
				},
			},
		},
		Scheduling: &compute.Scheduling{
			MaxRunDuration: &compute.Duration{
				Seconds: 15 * 60,
			},
			InstanceTerminationAction: "DELETE",
		},
	}

	// instance is a pointer so we can update it after setting it
	opBuilder := p.client.Instances.Insert(p.projectID, p.zone, instance).Context(ctx)

	if p.template == "" {
		instance.MachineType = fmt.Sprintf("zones/%s/machineTypes/e2-medium", p.zone)
		instance.Disks = []*compute.AttachedDisk{
			{
				AutoDelete: true,
				Boot:       true,
				InitializeParams: &compute.AttachedDiskInitializeParams{
					SourceImage: "projects/ubuntu-os-cloud/global/images/family/ubuntu-2204-lts",
				},
			},
		}
		instance.NetworkInterfaces = []*compute.NetworkInterface{
			{
				Network: "global/networks/default",
				AccessConfigs: []*compute.AccessConfig{
					{
						Name:        "External NAT",
						Type:        "ONE_TO_ONE_NAT",
						NetworkTier: "STANDARD",
					},
				},
			},
		}
		instance.ServiceAccounts = []*compute.ServiceAccount{
			{
				Email: "default",
				Scopes: []string{
					compute.ComputeScope,
				},
			},
		}
	} else {
		opBuilder = opBuilder.SourceInstanceTemplate(p.template)
	}

	op, err := opBuilder.Do()
	if err != nil {
		return fmt.Errorf("create instance: %w", err)
	}

	err = p.waitOperation(ctx, op)
	if err != nil {
		return fmt.Errorf("wait for instance creation: %w", err)
	}

	// Wait for instance to stop (indicating setup is complete)
	for {
		inst, err := p.client.Instances.Get(p.projectID, p.zone, instanceName).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("get instance status: %w", err)
		}

		if inst.Status == "TERMINATED" {
			break
		}

		time.Sleep(5 * time.Second)
	}

	// Create new image
	newImageName := fmt.Sprintf("%s-%s", typeLabelValue, lo.RandomString(5, lo.LowerCaseLettersCharset))
	imageOp, err := p.client.Images.Insert(p.projectID, &compute.Image{
		Name: newImageName,
		Labels: map[string]string{
			"type": typeLabelValue,
		},
		SourceDisk: fmt.Sprintf("projects/%s/zones/%s/disks/%s",
			p.projectID, p.zone, instanceName),
		StorageLocations: []string{
			getRegionFromZone(p.zone),
		},
	}).Context(ctx).Do()

	if err != nil {
		return fmt.Errorf("create new image: %w", err)
	}

	// Wait for new image creation to complete
	err = p.waitOperation(ctx, imageOp)
	if err != nil {
		return fmt.Errorf("wait for new image creation: %w", err)
	}

	// Delete old images
	images, err := p.client.Images.List(p.projectID).Filter(typeLabelFilter).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("list old images: %w", err)
	}

	for _, image := range images.Items {
		if image.Name != newImageName {
			_, err := p.client.Images.Delete(p.projectID, image.Name).Context(ctx).Do()
			if err != nil {
				return fmt.Errorf("delete old image %s: %w", image.Name, err)
			}
		}
	}

	// Delete the preparation instance
	_, err = p.client.Instances.Delete(p.projectID, p.zone, instanceName).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("delete instance: %w", err)
	}

	return nil
}

func (p *Provider) CreateRunner(ctx context.Context, url, token, labels string) error {
	instanceName := fmt.Sprintf("actions-runner-ephemeral-%s", lo.RandomString(5, lo.LowerCaseLettersCharset))
	cloudInitConf := common.GetCloudInitStart(url, token, labels)

	latestImage, err := p.getLatestImage(ctx)
	if err != nil {
		return fmt.Errorf("get latest image: %w", err)
	}
	if latestImage == nil {
		return fmt.Errorf("no runner image found")
	}

	instance := &compute.Instance{
		Name: instanceName,
		Labels: map[string]string{
			"type":   typeLabelValue,
			"status": labelStatusStarting,
		},
		Metadata: &compute.Metadata{
			Items: []*compute.MetadataItems{
				{
					Key:   "user-data",
					Value: &cloudInitConf,
				},
			},
		},
	}

	// instance is a pointer so we can update it after setting it
	opBuilder := p.client.Instances.Insert(p.projectID, p.zone, instance).Context(ctx)

	sourceImagePath := fmt.Sprintf("projects/%s/global/images/%s", p.projectID, latestImage.Name)

	if p.template == "" {
		instance.MachineType = fmt.Sprintf("zones/%s/machineTypes/e2-medium", p.zone)
		instance.Disks = []*compute.AttachedDisk{
			{
				AutoDelete: true,
				Boot:       true,
				InitializeParams: &compute.AttachedDiskInitializeParams{
					SourceImage: sourceImagePath,
				},
			},
		}
		instance.NetworkInterfaces = []*compute.NetworkInterface{
			{
				Network: "global/networks/default",
				AccessConfigs: []*compute.AccessConfig{
					{
						Name:        "External NAT",
						Type:        "ONE_TO_ONE_NAT",
						NetworkTier: "STANDARD",
					},
				},
			},
		}
		instance.ServiceAccounts = []*compute.ServiceAccount{
			{
				Email: "default",
				Scopes: []string{
					compute.ComputeScope,
				},
			},
		}
	} else {
		template, err := p.client.InstanceTemplates.Get(p.projectID, p.template).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("get instance template: %w", err)
		}

		// Copy the template disks but override the boot disk's source image.
		// This would allow for other disks (like local SSDs) to be attached.
		instance.Disks = template.Properties.Disks
		for _, disk := range instance.Disks {
			if disk.Boot {
				disk.InitializeParams.SourceImage = sourceImagePath
				break
			}
		}
		opBuilder = opBuilder.SourceInstanceTemplate(p.template)
	}

	op, err := opBuilder.Do()
	if err != nil {
		return fmt.Errorf("create instance: %w", err)
	}

	err = p.waitOperation(ctx, op)
	if err != nil {
		return fmt.Errorf("wait for instance creation: %w", err)
	}

	return nil
}

// DeleteRunners deletes N runner instances
func (p *Provider) DeleteRunners(ctx context.Context, count int, wait bool) error {
	listRes, err := p.client.Instances.List(p.projectID, p.zone).Filter(typeLabelFilter).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("listing instances: %w", err)
	}

	var instances []*compute.Instance
	for _, instance := range listRes.Items {
		if instance.Labels["status"] != "active" {
			instances = append(instances, instance)
		}
	}

	// Create a slice to store operations
	var operations []*compute.Operation

	// Delete up to count instances
	for i := 0; i < count && i < len(instances); i++ {
		op, err := p.client.Instances.Delete(p.projectID, p.zone, instances[i].Name).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("deleting instance %s: %w", instances[i].Name, err)
		}

		if wait {
			err = p.waitOperation(ctx, op)
			if err != nil {
				return fmt.Errorf("waiting for instance deletion %s: %w", instances[i].Name, err)
			}
		}
		operations = append(operations, op)
	}

	// Wait for all operations to complete if wait is true
	if wait {
		for _, op := range operations {
			err := p.waitOperation(ctx, op)
			if err != nil {
				return fmt.Errorf("waiting for instance deletion: %w", err)
			}
		}
	}

	return nil
}

func (p *Provider) waitOperation(ctx context.Context, op *compute.Operation) error {
	for {
		// sleep first since operations may 404 after creation
		time.Sleep(5 * time.Second)

		var result *compute.Operation
		var err error

		if op.Zone != "" {
			result, err = p.client.ZoneOperations.Get(p.projectID, p.zone, op.Name).Context(ctx).Do()
		} else {
			result, err = p.client.GlobalOperations.Get(p.projectID, op.Name).Context(ctx).Do()
		}

		if err != nil {
			return fmt.Errorf("get operation: %w", err)
		}

		if result.Status == "DONE" {
			if result.Error != nil {
				return fmt.Errorf("operation failed: %v", result.Error)
			}
			return nil
		}
	}
}

type disposition struct {
	startingCount  int
	idleCount      int
	activeCount    int
	preparingCount int
	stoppedCount   int
}

func (d disposition) TotalCount() int {
	return d.activeCount + d.idleCount + d.startingCount + d.stoppedCount
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
func (d disposition) PreparingCount() int {
	return d.preparingCount
}

func (p *Provider) RunnerDisposition(ctx context.Context) (interfaces.RunnerDispositionMetrics, error) {
	listRes, err := p.client.Instances.List(p.projectID, p.zone).Filter(typeLabelFilter).Context(ctx).Do()
	if err != nil {
		return disposition{}, fmt.Errorf("getting instances: %w", err)
	}
	res := disposition{}
	for _, instance := range listRes.Items {
		switch instance.Status {
		case "STOPPING", "STOPPED", "TERMINATED":
			res.stoppedCount++
			continue
		}

		labelStatus := instance.Labels["status"]
		switch labelStatus {
		case "active":
			res.activeCount++
		case "idle":
			res.idleCount++
		case labelStatusPreparing:
			res.preparingCount++
		case labelStatusStarting:
			res.startingCount++
		}
	}

	return res, nil
}
