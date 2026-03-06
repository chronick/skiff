package runtime

import "context"

// ContainerRuntime abstracts container operations for different runtimes.
type ContainerRuntime interface {
	// Run starts a container with the given config.
	Run(ctx context.Context, name string, cfg ContainerConfig) error
	// Stop stops a running container.
	Stop(ctx context.Context, name string) error
	// Build builds a container image.
	Build(ctx context.Context, name string, cfg ContainerConfig) error
	// Exec runs a command inside a running container.
	Exec(ctx context.Context, name string, command []string) ([]byte, error)
	// List returns all managed containers.
	List(ctx context.Context) ([]ContainerInfo, error)
	// Inspect returns details for a single container.
	Inspect(ctx context.Context, name string) (*ContainerInfo, error)
	// InjectDNS modifies a container config to use the plane DNS server.
	InjectDNS(cfg ContainerConfig, dnsIP string, dnsPort int) ContainerConfig
	// SetLimits applies resource limits to a container config.
	SetLimits(cfg ContainerConfig, limits ResourceLimits) ContainerConfig
}

// ContainerConfig holds container start parameters.
type ContainerConfig struct {
	Image      string
	Dockerfile string
	Context    string
	Volumes    []string
	Env        map[string]string
	Ports      []string
	CPUs       float64
	Memory     string
}

// ContainerInfo is the status of a container.
type ContainerInfo struct {
	Name  string   `json:"name"`
	Image string   `json:"image"`
	State string   `json:"state"`
	Ports []string `json:"ports"`
}

// ResourceLimits defines CPU and memory constraints.
type ResourceLimits struct {
	CPUs   float64 // e.g., 1.5 = 1.5 cores
	Memory string  // e.g., "512m", "2g"
}
