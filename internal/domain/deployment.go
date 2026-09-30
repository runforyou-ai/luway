package domain

// DeploymentMode 定义服务端的部署形态。
type DeploymentMode string

const (
	DeploymentModeSelfHosted DeploymentMode = "self_hosted"
	DeploymentModeManaged    DeploymentMode = "managed"
)

// Valid 判断部署形态是否为受支持的取值。
func (mode DeploymentMode) Valid() bool {
	return mode == DeploymentModeSelfHosted || mode == DeploymentModeManaged
}

// Managed 判断是否为官方托管部署。
func (mode DeploymentMode) Managed() bool {
	return mode == DeploymentModeManaged
}
