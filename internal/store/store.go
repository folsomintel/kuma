package store

// Store persists devices and cloud agents.
type Store interface {
	CreateDevice(d Device) error
	GetDevice(id string) (Device, error)
	ListDevices() []Device
	DeleteDevice(id string) error

	CreateCloudAgent(a CloudAgent) error
	GetCloudAgent(id string) (CloudAgent, error)
	ListCloudAgents() []CloudAgent
	UpdateCloudAgent(a CloudAgent) error
	DeleteCloudAgent(id string) error
}

// Kind identifies how a device was provisioned.
type Kind string

const (
	KindDevice Kind = "device"
	KindCloud  Kind = "cloud"
)
