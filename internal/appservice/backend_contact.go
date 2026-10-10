package appservice

import "context"

// ContactBackend 定义联系人、联系人字段与标签的业务调用。
type ContactBackend interface {
	// ListContacts 返回联系人列表。
	//appservice:route GET /contacts perm=none
	ListContacts(context.Context, RequestMeta, ContactListInput) (ContactList, error)
	// GetContact 返回联系人详情。
	//appservice:route GET /contacts/{contactID:uuid} perm=none
	GetContact(context.Context, RequestMeta, string) (Contact, error)
	// CreateContact 创建联系人。
	//appservice:route POST /contacts status=201 perm=external_contacts.manage
	CreateContact(context.Context, RequestMeta, ContactInput) (Contact, error)
	// UpdateContact 修改联系人。
	//appservice:route PUT /contacts/{contactID:uuid} perm=external_contacts.manage
	UpdateContact(context.Context, RequestMeta, string, ContactInput) (Contact, error)
	// DeleteContact 将联系人移入回收站。
	//appservice:route DELETE /contacts/{contactID:uuid} perm=external_contacts.manage
	DeleteContact(context.Context, RequestMeta, string) error
	// RestoreContact 恢复联系人。
	//appservice:route POST /contacts/{contactID:uuid}/restore perm=external_contacts.manage
	RestoreContact(context.Context, RequestMeta, string) (Contact, error)
	// SetContactFieldValue 由客服填写或清空联系人字段。
	//appservice:route PUT /contacts/{contactID:uuid}/fields/{fieldID:uuid} perm=external_contacts.manage
	SetContactFieldValue(context.Context, RequestMeta, string, string, ContactFieldValueInput) error
	// AddContactTag 由客服给联系人添加标签。
	//appservice:route PUT /contacts/{contactID:uuid}/tags/{tagID:uuid} perm=external_contacts.manage
	AddContactTag(context.Context, RequestMeta, string, string) error
	// RemoveContactTag 由客服移除联系人上的标签。
	//appservice:route DELETE /contacts/{contactID:uuid}/tags/{tagID:uuid} perm=external_contacts.manage
	RemoveContactTag(context.Context, RequestMeta, string, string) error
	// ListContactFields 返回当前企业的联系人字段。
	//appservice:route GET /settings/customer-service/contact-fields perm=none
	ListContactFields(context.Context, RequestMeta) (ContactFieldList, error)
	// CreateContactField 新增联系人字段。
	//appservice:route POST /settings/customer-service/contact-fields status=201 perm=customer_service.manage
	CreateContactField(context.Context, RequestMeta, ContactFieldInput) (ContactField, error)
	// UpdateContactField 修改联系人字段，被移除的单选选项对应的取值随之清空。
	//appservice:route PUT /settings/customer-service/contact-fields/{fieldID:uuid} perm=customer_service.manage
	UpdateContactField(context.Context, RequestMeta, string, ContactFieldInput) (ContactField, error)
	// DeleteContactField 删除联系人字段及其全部取值。
	//appservice:route DELETE /settings/customer-service/contact-fields/{fieldID:uuid} perm=customer_service.manage
	DeleteContactField(context.Context, RequestMeta, string) error
	// ListContactTags 返回当前企业的联系人标签。
	//appservice:route GET /settings/customer-service/contact-tags perm=none
	ListContactTags(context.Context, RequestMeta) (ContactTagList, error)
	// CreateContactTag 新增联系人标签。
	//appservice:route POST /settings/customer-service/contact-tags status=201 perm=customer_service.manage
	CreateContactTag(context.Context, RequestMeta, ContactTagInput) (ContactTag, error)
	// UpdateContactTag 修改联系人标签。
	//appservice:route PUT /settings/customer-service/contact-tags/{tagID:uuid} perm=customer_service.manage
	UpdateContactTag(context.Context, RequestMeta, string, ContactTagInput) (ContactTag, error)
	// DeleteContactTag 删除联系人标签并从所有联系人上移除。
	//appservice:route DELETE /settings/customer-service/contact-tags/{tagID:uuid} perm=customer_service.manage
	DeleteContactTag(context.Context, RequestMeta, string) error
}
