// layercheck 校验各构建标签下后端生产代码的分层依赖方向。
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// internalPrefix 是仓库内部包的导入路径前缀。
const internalPrefix = "github.com/runforyou-ai/luway/internal/"

// dependencyRule 声明来源包允许或禁止依赖的内部包及其子包。
type dependencyRule struct {
	sources []string
	allowed []string
	denied  []string
}

// dependencyRules 是后端各层的依赖方向规则。
var dependencyRules = []dependencyRule{
	{sources: []string{"domain", "common"}, allowed: []string{"domain", "common"}},
	{sources: []string{"agentcontract"}, allowed: []string{"domain", "common", "agentcontract"}},
	{sources: []string{"integration", "agentruntime", "knowledgeretrieval"}, denied: []string{"actions", "storage", "appservice", "api", "httpcodec", "native", "task", "realtime"}},
	{sources: []string{"storage"}, denied: []string{"actions", "appservice", "api", "httpcodec", "native"}},
	{sources: []string{"actions"}, denied: []string{"appservice", "api", "httpcodec", "native"}},
	{sources: []string{"actions/serverinstance", "actions/serverlog"}, denied: []string{"actions"}},
}

// listedPackage 接收 go list 输出的包路径及直接生产依赖。
type listedPackage struct {
	ImportPath string
	Imports    []string
}

// main 按构建标签读取生产依赖，输出违规引用并以非零状态退出。
func main() {
	failed := false
	for _, tags := range []string{"", "testhash", "server", "server,testhash"} {
		command := exec.Command("go", "list", "-json", "-tags="+tags, "./internal/...")
		command.Stderr = os.Stderr
		output, err := command.Output()
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取生产依赖失败（tags=%q）：%v\n", tags, err)
			os.Exit(1)
		}
		violations, err := checkDependencies(bytes.NewReader(output))
		if err != nil {
			fmt.Fprintf(os.Stderr, "解析生产依赖失败（tags=%q）：%v\n", tags, err)
			os.Exit(1)
		}
		for _, violation := range violations {
			fmt.Fprintf(os.Stderr, "分层依赖违规（tags=%q）：%s\n", tags, violation)
			failed = true
		}
	}
	if failed {
		os.Exit(1)
	}
	fmt.Println("后端分层依赖检查通过")
}

// checkDependencies 读取连续的 go list JSON 对象并返回按路径排序的违规生产依赖。
func checkDependencies(input io.Reader) ([]string, error) {
	decoder := json.NewDecoder(input)
	var violations []string
	for {
		var pkg listedPackage
		if err := decoder.Decode(&pkg); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		for _, rule := range dependencyRules {
			if !matchesPackage(pkg.ImportPath, rule.sources) {
				continue
			}
			for _, dependency := range pkg.Imports {
				if !strings.HasPrefix(dependency, internalPrefix) {
					continue
				}
				if (len(rule.allowed) > 0 && !matchesPackage(dependency, rule.allowed)) || matchesPackage(dependency, rule.denied) {
					violations = append(violations, pkg.ImportPath+" → "+dependency)
				}
			}
		}
	}
	slices.Sort(violations)
	return slices.Compact(violations), nil
}

// matchesPackage 判断内部包路径是否属于给定包或其子包。
func matchesPackage(path string, packages []string) bool {
	for _, pkg := range packages {
		prefix := internalPrefix + pkg
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}
