//go:build server

package businesssystem

import (
	"net/http"
	"slices"
	"strings"

	"github.com/runforyou-ai/luway/internal/common"
	"github.com/runforyou-ai/luway/internal/domain"
	"github.com/runforyou-ai/support"
	"github.com/runforyou-ai/support/str"
	"golang.org/x/net/http/httpguts"
)

// ValidationCode 标识业务系统字段校验结果。
type ValidationCode = common.FieldCode

const (
	ValidationTransportInvalid        ValidationCode = "BUSINESS_SYSTEM_TRANSPORT_INVALID"
	ValidationTransportChanged        ValidationCode = "BUSINESS_SYSTEM_TRANSPORT_CHANGED"
	ValidationServerTypeInvalid       ValidationCode = "BUSINESS_SYSTEM_SERVER_TYPE_INVALID"
	ValidationNameDuplicate           ValidationCode = "BUSINESS_SYSTEM_NAME_DUPLICATE"
	ValidationURLRequired             ValidationCode = "BUSINESS_SYSTEM_URL_REQUIRED"
	ValidationURLInvalid              ValidationCode = "BUSINESS_SYSTEM_URL_INVALID"
	ValidationURLTooLong              ValidationCode = "BUSINESS_SYSTEM_URL_TOO_LONG"
	ValidationSpecRequired            ValidationCode = "BUSINESS_SYSTEM_SPEC_REQUIRED"
	ValidationSpecInvalid             ValidationCode = "BUSINESS_SYSTEM_SPEC_INVALID"
	ValidationSpecEmpty               ValidationCode = "BUSINESS_SYSTEM_SPEC_EMPTY"
	ValidationBaseURLRequired         ValidationCode = "BUSINESS_SYSTEM_BASE_URL_REQUIRED"
	ValidationCredentialInvalid       ValidationCode = "BUSINESS_SYSTEM_CREDENTIAL_INVALID"
	ValidationCredentialHeaderInvalid ValidationCode = "BUSINESS_SYSTEM_CREDENTIAL_HEADER_INVALID"
	ValidationCredentialRequired      ValidationCode = "BUSINESS_SYSTEM_CREDENTIAL_REQUIRED"
	ValidationHeaderBindingInvalid    ValidationCode = "BUSINESS_SYSTEM_HEADER_BINDING_INVALID"
	ValidationToolBindingInvalid      ValidationCode = "BUSINESS_SYSTEM_TOOL_BINDING_INVALID"
)

// ValidationError 表示业务系统字段校验失败。
type ValidationError = common.FieldError

const (
	// maxURLBytes 是业务系统地址的最大字节数。
	maxURLBytes = 2048
)

// normalizeInput 归一化并校验业务系统的连接配置与请求头绑定，请求头名称转为规范写法。
func normalizeInput(input Input) (Input, map[string]ValidationCode) {
	input.Name = strings.TrimSpace(input.Name)
	connection, fields := normalizeConnectionInput(input.Connection)
	input.Connection = connection
	// 请求头绑定只接受合法的请求头名称与已定义的上下文值，认证请求头由凭据单独配置。
	credentialHeader, _ := connection.Credential.Header()
	bindings := make(map[string]domain.ContextValue, len(input.HeaderBindings))
	for name, value := range input.HeaderBindings {
		canonical := http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !httpguts.ValidHeaderFieldName(canonical) || canonical == "Authorization" || canonical == credentialHeader || !value.Valid() {
			fields["headerBindings"] = ValidationHeaderBindingInvalid
			continue
		}
		bindings[canonical] = value
	}
	input.HeaderBindings = bindings
	return input, fields
}

// normalizeConnectionInput 按传输方式校验连接配置并只保留对应的一项，再校验凭据并清除认证方式用不到的字段。
func normalizeConnectionInput(input ConnectionInput) (ConnectionInput, map[string]ValidationCode) {
	fields := make(map[string]ValidationCode)
	switch input.Transport {
	case domain.BusinessSystemTransportMCP:
		connection := support.Deref(input.Connection.MCP)
		connection.URL = strings.TrimSpace(connection.URL)
		validateURL(fields, "url", connection.URL, true)
		if connection.ServerType != domain.MCPServerTypeSSE && connection.ServerType != domain.MCPServerTypeStreamableHTTP {
			fields["serverType"] = ValidationServerTypeInvalid
		}
		input.Connection = domain.BusinessSystemConnection{MCP: &connection}
	case domain.BusinessSystemTransportHTTP:
		connection := support.Deref(input.Connection.HTTP)
		connection.BaseURL = strings.TrimRight(strings.TrimSpace(connection.BaseURL), "/")
		connection.SpecURL = strings.TrimSpace(connection.SpecURL)
		validateURL(fields, "baseUrl", connection.BaseURL, false)
		// 文档地址与文档正文二选一，填写地址时不保存正文。
		if connection.SpecURL != "" {
			validateURL(fields, "specUrl", connection.SpecURL, true)
			connection.Spec = ""
		} else if strings.TrimSpace(connection.Spec) == "" {
			fields["spec"] = ValidationSpecRequired
		}
		input.Connection = domain.BusinessSystemConnection{HTTP: &connection}
	default:
		fields["transport"] = ValidationTransportInvalid
	}
	input.Credential = normalizeCredential(fields, input.Credential)
	return input, fields
}

// validateURL 校验地址字段，required 为 false 时允许为空。
func validateURL(fields map[string]ValidationCode, field, value string, required bool) {
	switch {
	case value == "":
		if required {
			fields[field] = ValidationURLRequired
		}
	case len(value) > maxURLBytes:
		fields[field] = ValidationURLTooLong
	case !str.IsHTTPURL(value):
		fields[field] = ValidationURLInvalid
	}
}

// normalizeCredential 校验凭据：令牌方式需要令牌，请求头方式另需合法的非 Authorization 请求头名称，Basic 方式需要用户名；只保留认证方式用到的字段。
func normalizeCredential(fields map[string]ValidationCode, credential domain.BusinessSystemCredential) domain.BusinessSystemCredential {
	switch credential.Kind {
	case domain.BusinessSystemCredentialNone:
		return domain.BusinessSystemCredential{Kind: credential.Kind}
	case domain.BusinessSystemCredentialBearer, domain.BusinessSystemCredentialHeader:
		normalized := domain.BusinessSystemCredential{Kind: credential.Kind, Token: strings.TrimSpace(credential.Token)}
		if normalized.Token == "" {
			fields["credentialToken"] = ValidationCredentialRequired
		}
		if credential.Kind == domain.BusinessSystemCredentialHeader {
			normalized.HeaderName = http.CanonicalHeaderKey(strings.TrimSpace(credential.HeaderName))
			if !httpguts.ValidHeaderFieldName(normalized.HeaderName) || normalized.HeaderName == "Authorization" {
				fields["credentialHeaderName"] = ValidationCredentialHeaderInvalid
			}
		}
		return normalized
	case domain.BusinessSystemCredentialBasic:
		normalized := domain.BusinessSystemCredential{Kind: credential.Kind, Username: strings.TrimSpace(credential.Username), Password: credential.Password}
		if normalized.Username == "" {
			fields["credentialUsername"] = ValidationCredentialRequired
		}
		return normalized
	}
	fields["credential"] = ValidationCredentialInvalid
	return credential
}

// validateToolSetting 校验参数绑定指向工具的顶层参数且使用已定义的上下文值。
func validateToolSetting(tool domain.BusinessTool, setting domain.BusinessToolSetting) map[string]ValidationCode {
	parameters := tool.Parameters()
	for name, value := range setting.ParameterBindings {
		if !slices.Contains(parameters, name) || !value.Valid() {
			return map[string]ValidationCode{"parameterBindings": ValidationToolBindingInvalid}
		}
	}
	return nil
}
