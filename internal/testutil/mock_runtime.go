package testutil

import (
	"context"
	"fmt"
	"sync"

	"github.com/chronick/skiff/internal/runtime"
)

// RuntimeCall records a single invocation on the mock runtime.
type RuntimeCall struct {
	Method string
	Name   string
	Args   interface{} // method-specific args
}

// MockContainerRuntime is a test double for runtime.ContainerRuntime.
type MockContainerRuntime struct {
	mu    sync.Mutex
	Calls []RuntimeCall

	RunErr           error
	StopErr          error
	BuildErr         error
	ExecOutput       []byte
	ExecErr          error
	ListResult       []runtime.ContainerInfo
	ListErr          error
	InspectResult    *runtime.ContainerInfo
	InspectErr       error
	StatsResult      *runtime.ContainerStats
	StatsErr         error
	LogsOutput       []byte
	LogsErr          error
	CreateNetworkErr error
	DeleteNetworkErr error
}

func NewMockRuntime() *MockContainerRuntime {
	return &MockContainerRuntime{}
}

func (m *MockContainerRuntime) record(method, name string, args interface{}) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Calls = append(m.Calls, RuntimeCall{Method: method, Name: name, Args: args})
}

func (m *MockContainerRuntime) Run(ctx context.Context, name string, cfg runtime.ContainerConfig) error {
	m.record("Run", name, cfg)
	return m.RunErr
}

func (m *MockContainerRuntime) Stop(ctx context.Context, name string) error {
	m.record("Stop", name, nil)
	return m.StopErr
}

func (m *MockContainerRuntime) Build(ctx context.Context, name string, cfg runtime.ContainerConfig) error {
	m.record("Build", name, cfg)
	return m.BuildErr
}

func (m *MockContainerRuntime) Exec(ctx context.Context, name string, command []string) ([]byte, error) {
	m.record("Exec", name, command)
	return m.ExecOutput, m.ExecErr
}

func (m *MockContainerRuntime) List(ctx context.Context) ([]runtime.ContainerInfo, error) {
	m.record("List", "", nil)
	return m.ListResult, m.ListErr
}

func (m *MockContainerRuntime) Inspect(ctx context.Context, name string) (*runtime.ContainerInfo, error) {
	m.record("Inspect", name, nil)
	return m.InspectResult, m.InspectErr
}

func (m *MockContainerRuntime) InjectDNS(cfg runtime.ContainerConfig, dnsIP string, dnsPort int) runtime.ContainerConfig {
	m.record("InjectDNS", "", map[string]interface{}{"dnsIP": dnsIP, "dnsPort": dnsPort})
	return cfg
}

func (m *MockContainerRuntime) SetLimits(cfg runtime.ContainerConfig, limits runtime.ResourceLimits) runtime.ContainerConfig {
	m.record("SetLimits", "", limits)
	if limits.CPUs > 0 {
		cfg.CPUs = limits.CPUs
	}
	if limits.Memory != "" {
		cfg.Memory = limits.Memory
	}
	return cfg
}

func (m *MockContainerRuntime) Stats(ctx context.Context, name string) (*runtime.ContainerStats, error) {
	m.record("Stats", name, nil)
	return m.StatsResult, m.StatsErr
}

func (m *MockContainerRuntime) Logs(ctx context.Context, name string, lines int) ([]byte, error) {
	m.record("Logs", name, lines)
	return m.LogsOutput, m.LogsErr
}

func (m *MockContainerRuntime) CreateNetwork(ctx context.Context, name string, cfg runtime.NetworkConfig) error {
	m.record("CreateNetwork", name, cfg)
	return m.CreateNetworkErr
}

func (m *MockContainerRuntime) DeleteNetwork(ctx context.Context, name string) error {
	m.record("DeleteNetwork", name, nil)
	return m.DeleteNetworkErr
}

func (m *MockContainerRuntime) CallsFor(method string) []RuntimeCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []RuntimeCall
	for _, c := range m.Calls {
		if c.Method == method {
			result = append(result, c)
		}
	}
	return result
}

func (m *MockContainerRuntime) CallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.Calls)
}

func (m *MockContainerRuntime) LastCall() (RuntimeCall, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.Calls) == 0 {
		return RuntimeCall{}, fmt.Errorf("no calls recorded")
	}
	return m.Calls[len(m.Calls)-1], nil
}
