package appservice

import "context"

// IntegrationBackend 定义工作区模型服务与业务系统的业务调用。
type IntegrationBackend interface {
	// ListAIModelOptions 返回当前工作区满足指定用途的模型。
	//appservice:route GET /ai-models query=usage perm=none
	ListAIModelOptions(context.Context, RequestMeta, AIModelUsage) (AIModelOptionList, error)
	// ListAIProviders 返回当前企业的模型服务供应商列表。
	//appservice:route GET /settings/model-services perm=workspace.manage
	ListAIProviders(context.Context, RequestMeta) (AIProviderList, error)
	// GetAIProvider 返回当前企业中的模型服务供应商详情。
	//appservice:route GET /settings/model-services/{providerID:uuid} perm=workspace.manage
	GetAIProvider(context.Context, RequestMeta, string) (AIProvider, error)
	// ListAvailableAIModels 返回指定品牌的预设模型目录。
	//appservice:route GET /settings/model-services/models query=brand perm=workspace.manage
	ListAvailableAIModels(context.Context, RequestMeta, AIProviderBrand) (AIProviderModelList, error)
	// DiscoverAIProviderModels 读取模型服务实例当前可用的模型目录。
	//appservice:route POST /settings/model-services/discover-models perm=workspace.manage
	DiscoverAIProviderModels(context.Context, RequestMeta, AIProviderConnectionInput) (AIProviderModelList, error)
	// TestAIProviderConnection 测试模型服务供应商草稿配置。
	//appservice:route POST /settings/model-services/test perm=workspace.manage
	TestAIProviderConnection(context.Context, RequestMeta, AIProviderConnectionInput) error
	// CreateAIProvider 创建模型服务供应商。
	//appservice:route POST /settings/model-services status=201 perm=workspace.manage
	CreateAIProvider(context.Context, RequestMeta, AIProviderInput) (AIProvider, error)
	// UpdateAIProvider 修改模型服务供应商。
	//appservice:route PUT /settings/model-services/{providerID:uuid} perm=workspace.manage
	UpdateAIProvider(context.Context, RequestMeta, string, AIProviderUpdateInput) (AIProvider, error)
	// DeleteAIProvider 删除模型服务供应商。
	//appservice:route DELETE /settings/model-services/{providerID:uuid} perm=workspace.manage
	DeleteAIProvider(context.Context, RequestMeta, string) error
	// ListBusinessSystems 返回当前工作区配置的业务系统。
	//appservice:route GET /settings/business-systems perm=workspace.manage
	ListBusinessSystems(context.Context, RequestMeta) (BusinessSystemList, error)
	// GetBusinessSystem 返回当前工作区中的业务系统详情。
	//appservice:route GET /settings/business-systems/{businessSystemID:uuid} perm=workspace.manage
	GetBusinessSystem(context.Context, RequestMeta, string) (BusinessSystem, error)
	// TestBusinessSystemConnection 测试业务系统草稿连接配置。
	//appservice:route POST /settings/business-systems/test-connection perm=workspace.manage
	TestBusinessSystemConnection(context.Context, RequestMeta, BusinessSystemConnectionInput) error
	// TestSavedBusinessSystemConnection 测试已保存的业务系统。
	//appservice:route POST /settings/business-systems/{businessSystemID:uuid}/test-connection perm=workspace.manage
	TestSavedBusinessSystemConnection(context.Context, RequestMeta, string) error
	// RefreshBusinessSystemTools 提交当前工作区全部业务系统的工具目录更新任务。
	//appservice:route POST /settings/business-systems/refresh-tools perm=workspace.manage
	RefreshBusinessSystemTools(context.Context, RequestMeta) error
	// CreateBusinessSystem 创建业务系统。
	//appservice:route POST /settings/business-systems status=201 perm=workspace.manage
	CreateBusinessSystem(context.Context, RequestMeta, BusinessSystemInput) (BusinessSystem, error)
	// UpdateBusinessSystem 修改业务系统。
	//appservice:route PUT /settings/business-systems/{businessSystemID:uuid} perm=workspace.manage
	UpdateBusinessSystem(context.Context, RequestMeta, string, BusinessSystemInput) (BusinessSystem, error)
	// UpdateBusinessToolSetting 保存业务系统中一个工具的事实修正、停用与参数绑定。
	//appservice:route PUT /settings/business-systems/{businessSystemID:uuid}/tool-setting perm=workspace.manage
	UpdateBusinessToolSetting(context.Context, RequestMeta, string, BusinessToolSettingInput) (BusinessSystem, error)
	// DeleteBusinessSystem 删除业务系统。
	//appservice:route DELETE /settings/business-systems/{businessSystemID:uuid} perm=workspace.manage
	DeleteBusinessSystem(context.Context, RequestMeta, string) error
}
