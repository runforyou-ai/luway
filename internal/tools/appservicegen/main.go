// appservicegen 根据 Backend、ComputerBackend 和 WebsiteVisitorBackend 的路由指令生成服务端认证分发与权限校验、HTTP 路由、执行器客户端与前端 TypeScript 契约和调用函数，并根据实时协议的事件结构生成前端实时事件类型与解码模式。
// 指定 -contract 时为模块契约生成：模块的业务契约接口可以引用核心 appservice 契约的类型，生成的分发层与 HTTP 路由复用核心的认证、错误收尾与请求绑定。
package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"strings"
)

// corePath 是核心 appservice 契约包的导入路径。
const corePath = "github.com/runforyou-ai/luway/internal/appservice"

// moduleOptions 是模块契约的生成选项，路径相对模块根目录。
type moduleOptions struct {
	contract   string
	iface      string
	direct     string
	http       string
	typescript string
	domains    string
}

// main 解析契约并写出各层生成文件。
func main() {
	var options moduleOptions
	flag.StringVar(&options.contract, "contract", "", "模块契约包目录")
	flag.StringVar(&options.iface, "interface", "Backend", "模块契约包中的业务契约接口名")
	flag.StringVar(&options.direct, "direct", "", "认证分发层的输出文件")
	flag.StringVar(&options.http, "http", "", "HTTP 路由的输出文件")
	flag.StringVar(&options.typescript, "ts", "", "前端契约与调用函数的输出目录")
	flag.StringVar(&options.domains, "domains", "", "额外领域包的完整导入路径，以逗号分隔")
	flag.Parse()
	run := runCore
	if options.contract != "" {
		run = func() error { return runModule(options) }
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "appservicegen:", err)
		os.Exit(1)
	}
}

// runModule 加载模块契约与它引用的核心契约，解析业务契约接口并写出分发层、HTTP 路由与前端契约和调用函数。
func runModule(options moduleOptions) error {
	if options.direct == "" || options.http == "" || options.typescript == "" {
		return fmt.Errorf("module mode requires -direct, -http and -ts")
	}
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	contractDir := filepath.Join(root, options.contract)
	var domains []string
	for path := range strings.SplitSeq(options.domains, ",") {
		if path = strings.TrimSpace(path); path != "" {
			domains = append(domains, path)
		}
	}
	loaded, err := loadModuleContract(contractDir, domains...)
	if err != nil {
		return err
	}
	business, err := loaded.parseInterface(options.iface, businessContract)
	if err != nil {
		return err
	}
	permissionConsts, err := parsePermissionCodes(filepath.Join(loaded.domainDir, "permission.go"))
	if err != nil {
		return err
	}
	if err := resolvePermissions(business, permissionConsts); err != nil {
		return err
	}
	if err := loaded.validate(business, businessContract); err != nil {
		return err
	}
	// 模块契约的文案键可以来自模块与核心词条，核心仓库根目录是模块根目录之上最近的 go.mod 所在目录。
	coreRoot := filepath.Dir(root)
	for {
		if _, err := os.Stat(filepath.Join(coreRoot, "go.mod")); err == nil {
			break
		}
		if parent := filepath.Dir(coreRoot); parent != coreRoot {
			coreRoot = parent
			continue
		}
		return fmt.Errorf("core go.mod not found above module %s", root)
	}
	if err := checkValidation([]string{root, coreRoot}, business, businessContract); err != nil {
		return err
	}
	types, err := loaded.generateTypeScriptContract(business, filepath.ToSlash(options.contract))
	if err != nil {
		return err
	}
	direct := filepath.Join(root, options.direct)
	http := filepath.Join(root, options.http)
	goFiles := map[string][]byte{
		direct: loaded.generateDirectBackend(business, businessDispatch, filepath.Base(filepath.Dir(direct))),
		http:   loaded.generateHTTP(business, businessHTTP, filepath.Base(filepath.Dir(http))),
	}
	if err := writeGoFiles(goFiles); err != nil {
		return err
	}
	generated := filepath.Join(root, options.typescript)
	if err := os.MkdirAll(generated, 0o755); err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(generated, "contract.ts"), types); err != nil {
		return err
	}
	return writeIfChanged(filepath.Join(generated, "operations.ts"), loaded.generateTypeScriptOperations(business))
}

// runCore 加载 appservice 包与实时协议包，解析三份契约并写出生成文件。
func runCore() error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	loaded, err := loadContract(filepath.Join(root, "internal", "appservice"))
	if err != nil {
		return err
	}
	business, err := loaded.parseInterface("Backend", businessContract)
	if err != nil {
		return err
	}
	computer, err := loaded.parseInterface("ComputerBackend", computerContract)
	if err != nil {
		return err
	}
	visitor, err := loaded.parseInterface("WebsiteVisitorBackend", visitorContract)
	if err != nil {
		return err
	}
	permissionConsts, err := parsePermissionCodes(filepath.Join(root, "internal", "domain", "permission.go"))
	if err != nil {
		return err
	}
	if err := resolvePermissions(business, permissionConsts); err != nil {
		return err
	}
	if err := loaded.validate(business, businessContract); err != nil {
		return err
	}
	if err := loaded.validate(computer, computerContract); err != nil {
		return err
	}
	if err := loaded.validate(visitor, visitorContract); err != nil {
		return err
	}
	for kind, methods := range map[contractKind][]method{businessContract: business, computerContract: computer, visitorContract: visitor} {
		if err := checkValidation([]string{root}, methods, kind); err != nil {
			return err
		}
	}
	types, err := loaded.generateTypeScriptContract(business, "internal/appservice")
	if err != nil {
		return err
	}
	protocol, err := loadContract(filepath.Join(root, "internal", "realtime", "protocol"))
	if err != nil {
		return err
	}
	domainContract, err := loadContract(filepath.Join(root, "internal", "domain"))
	if err != nil {
		return err
	}
	realtime, err := generateRealtimeContract(protocol, domainContract, loaded)
	if err != nil {
		return err
	}
	goFiles := map[string][]byte{
		filepath.Join(root, "internal", "appservice", "direct", "backend_gen.go"):          loaded.generateDirectBackend(business, businessDispatch, "direct"),
		filepath.Join(root, "internal", "appservice", "direct", "computer_backend_gen.go"): loaded.generateDirectBackend(computer, computerDispatch, "direct"),
		filepath.Join(root, "internal", "api", "service_gen.go"):                           loaded.generateHTTP(business, businessHTTP, "api"),
		filepath.Join(root, "internal", "api", "computer_service_gen.go"):                  loaded.generateHTTP(computer, computerHTTP, "api"),
		filepath.Join(root, "internal", "api", "website_visitor_gen.go"):                   loaded.generateHTTP(visitor, websiteVisitorHTTP, "api"),
		filepath.Join(root, "internal", "executor", "client_gen.go"):                       loaded.generateExecutorClient(computer),
	}
	if err := writeGoFiles(goFiles); err != nil {
		return err
	}
	generated := filepath.Join(root, "frontend", "src", "api", "generated")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(generated, "contract.ts"), types); err != nil {
		return err
	}
	if err := writeIfChanged(filepath.Join(generated, "realtime.ts"), realtime); err != nil {
		return err
	}
	return writeIfChanged(filepath.Join(generated, "operations.ts"), loaded.generateTypeScriptOperations(business))
}

// writeGoFiles 格式化并写出生成的 Go 文件，按需创建所在目录。
func writeGoFiles(files map[string][]byte) error {
	for path, source := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		formatted, err := format.Source(source)
		if err != nil {
			return fmt.Errorf("format %s: %w\n%s", path, err, source)
		}
		if err := writeIfChanged(path, formatted); err != nil {
			return err
		}
	}
	return nil
}

// moduleRoot 从当前目录向上查找 go.mod 所在目录。
func moduleRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above working directory")
		}
		dir = parent
	}
}

// writeIfChanged 在内容变化时写入文件，内容相同时保留原文件的修改时间。
func writeIfChanged(path string, content []byte) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, content) {
		return nil
	}
	return os.WriteFile(path, content, 0o644)
}
