package fuse

import (
	"context"
	"fmt"
	"time"

	fusesdk "github.com/folsomintel/fuse/sdks/go"
)

// Environment is a subset of Fuse environment state used by kuma-api.
type Environment struct {
	ID        string
	TaskID    string
	State     string
	URL       string
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateParams are inputs for provisioning a cloud agent microVM.
type CreateParams struct {
	TaskID         string
	CPUs           int32
	RamMB          int32
	StorageGB      int32
	MaxRuntimeSecs int64
	Secrets        map[string]string
	StartupScript  string
	GatewayURL     string
	GatewayToken   string
}

// Client talks to a Fuse orchestrator.
type Client interface {
	CreateEnvironment(ctx context.Context, params CreateParams) (*Environment, error)
	GetEnvironment(ctx context.Context, id string) (*Environment, error)
	ListEnvironments(ctx context.Context) ([]Environment, error)
	DestroyEnvironment(ctx context.Context, id string) error
}

type sdkClient struct {
	inner *fusesdk.Client
}

// New creates a Fuse SDK-backed client.
func New(baseURL, token string) (Client, error) {
	inner, err := fusesdk.New(baseURL, token, fusesdk.WithUserAgent("kuma-api"))
	if err != nil {
		return nil, fmt.Errorf("fuse client: %w", err)
	}
	return &sdkClient{inner: inner}, nil
}

func (c *sdkClient) CreateEnvironment(ctx context.Context, params CreateParams) (*Environment, error) {
	req := fusesdk.CreateRequest{
		TaskID: params.TaskID,
		Spec: fusesdk.Spec{
			CPUs:              params.CPUs,
			RamMB:             params.RamMB,
			StorageGB:         params.StorageGB,
			MaxRuntimeSeconds: params.MaxRuntimeSecs,
		},
		Secrets:       params.Secrets,
		StartupScript: params.StartupScript,
		GatewayURL:    params.GatewayURL,
		GatewayToken:  params.GatewayToken,
	}
	env, err := c.inner.Environments.Create(ctx, req)
	if err != nil {
		return nil, err
	}
	return mapEnv(env), nil
}

func (c *sdkClient) GetEnvironment(ctx context.Context, id string) (*Environment, error) {
	env, err := c.inner.Environments.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return mapEnv(env), nil
}

func (c *sdkClient) ListEnvironments(ctx context.Context) ([]Environment, error) {
	list, err := c.inner.Environments.List(ctx, fusesdk.ListEnvironmentsOptions{})
	if err != nil {
		return nil, err
	}
	out := make([]Environment, 0, len(list))
	for i := range list {
		out = append(out, *mapEnv(&list[i]))
	}
	return out, nil
}

func (c *sdkClient) DestroyEnvironment(ctx context.Context, id string) error {
	return c.inner.Environments.Destroy(ctx, id)
}

func mapEnv(env *fusesdk.EnvironmentInfo) *Environment {
	if env == nil {
		return nil
	}
	return &Environment{
		ID:        env.ID,
		TaskID:    env.TaskID,
		State:     env.State,
		URL:       env.URL,
		Error:     env.Error,
		CreatedAt: env.CreatedAt,
		UpdatedAt: env.UpdatedAt,
	}
}
