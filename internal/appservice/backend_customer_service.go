package appservice

import "context"

// CustomerServiceBackend 定义服务会话侧栏资料与客服设置的业务调用。
type CustomerServiceBackend interface {
	// GetRequesterProfile 返回服务会话发起人的资料；发起人是客户时给出客户身份与当前周期访客上下文。
	//appservice:route GET /conversations/{conversationID:uuid}/requester-profile perm=none
	GetRequesterProfile(context.Context, RequestMeta, string) (RequesterProfile, error)
	// ListServiceBusinessQueries 返回服务会话当前周期内 AI 员工查询业务系统的记录。
	//appservice:route GET /conversations/{conversationID:uuid}/business-queries perm=none
	ListServiceBusinessQueries(context.Context, RequestMeta, string) (ServiceBusinessQueryList, error)
	// GetServiceSummaries 返回服务会话当前周期的交接摘要与同一发起人已关闭周期的小结。
	//appservice:route GET /conversations/{conversationID:uuid}/service-summaries perm=none
	GetServiceSummaries(context.Context, RequestMeta, string) (ServiceSummaries, error)
	// UpdateServiceSessionSummary 修改已关闭服务周期的小结、是否解决与咨询分类。
	//appservice:route PUT /service-sessions/{serviceSessionID:uuid}/summary perm=none
	UpdateServiceSessionSummary(context.Context, RequestMeta, string, ServiceSessionSummaryInput) (ServiceSessionSummary, error)
	// GetCustomerIdentitySecret 读取当前企业的客户身份密钥，未生成时为空。
	//appservice:route GET /settings/customer-service/identity-secret perm=customer_service.manage
	GetCustomerIdentitySecret(context.Context, RequestMeta) (CustomerIdentitySecret, error)
	// RegenerateCustomerIdentitySecret 生成或重新生成当前企业的客户身份密钥，旧密钥立即失效。
	//appservice:route POST /settings/customer-service/identity-secret perm=customer_service.manage
	RegenerateCustomerIdentitySecret(context.Context, RequestMeta) (CustomerIdentitySecret, error)
	// GetBusinessHours 读取当前企业的客服工作时间。
	//appservice:route GET /settings/customer-service/business-hours perm=customer_service.manage
	GetBusinessHours(context.Context, RequestMeta) (BusinessHours, error)
	// UpdateBusinessHours 修改当前企业的客服工作时间。
	//appservice:route PUT /settings/customer-service/business-hours perm=customer_service.manage
	UpdateBusinessHours(context.Context, RequestMeta, BusinessHours) (BusinessHours, error)
	// GetServiceTimeouts 读取当前企业的客服超时时长。
	//appservice:route GET /settings/customer-service/timeouts perm=customer_service.manage
	GetServiceTimeouts(context.Context, RequestMeta) (ServiceTimeouts, error)
	// UpdateServiceTimeouts 修改当前企业的客服超时时长。
	//appservice:route PUT /settings/customer-service/timeouts perm=customer_service.manage
	UpdateServiceTimeouts(context.Context, RequestMeta, ServiceTimeouts) (ServiceTimeouts, error)
	// GetServiceSummarySettings 读取当前企业的周期小结设置。
	//appservice:route GET /settings/customer-service/summary perm=customer_service.manage
	GetServiceSummarySettings(context.Context, RequestMeta) (ServiceSummarySettings, error)
	// UpdateServiceSummarySettings 修改当前企业的周期小结设置。
	//appservice:route PUT /settings/customer-service/summary perm=customer_service.manage
	UpdateServiceSummarySettings(context.Context, RequestMeta, ServiceSummarySettings) (ServiceSummarySettings, error)
	// ListServiceCategories 返回当前企业的咨询分类目录。
	//appservice:route GET /settings/customer-service/categories perm=none
	ListServiceCategories(context.Context, RequestMeta) (ServiceCategoryList, error)
	// CreateServiceCategory 新增咨询分类。
	//appservice:route POST /settings/customer-service/categories status=201 perm=customer_service.manage
	CreateServiceCategory(context.Context, RequestMeta, ServiceCategoryInput) (ServiceCategory, error)
	// UpdateServiceCategory 修改咨询分类。
	//appservice:route PUT /settings/customer-service/categories/{categoryID:uuid} perm=customer_service.manage
	UpdateServiceCategory(context.Context, RequestMeta, string, ServiceCategoryInput) (ServiceCategory, error)
	// DeleteServiceCategory 删除咨询分类，历史记录保留分类名称。
	//appservice:route DELETE /settings/customer-service/categories/{categoryID:uuid} perm=customer_service.manage
	DeleteServiceCategory(context.Context, RequestMeta, string) error
}
