//go:build server

package servercli

import (
	"fmt"
	"os"
	"strings"

	"github.com/goccy/go-yaml"
	serverconfig "github.com/runforyou-ai/luway/internal/config/server"
)

// configCommand 执行 config 的 path、show、check、set 子命令。
func configCommand(arguments []string) error {
	usage := "用法：config <path|show|check|set> [参数]\n  path   输出使用的配置文件路径\n  show   输出生效的配置，密码以星号代替\n  check  校验配置\n  set    修改配置文件中的一项配置：config set <配置项> <值>\n"
	if len(arguments) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return errUsage
	}
	switch arguments[0] {
	case "path":
		if err := parseFlags(newFlags("config path", ""), arguments[1:], 0); err != nil {
			return err
		}
		fmt.Println(serverconfig.Path())
		return nil
	case "show":
		if err := parseFlags(newFlags("config show", ""), arguments[1:], 0); err != nil {
			return err
		}
		config, err := serverconfig.Load()
		if err != nil {
			return err
		}
		output, err := yaml.Marshal(config.Redacted())
		if err != nil {
			return err
		}
		fmt.Print(string(output))
		return nil
	case "check":
		if err := parseFlags(newFlags("config check", ""), arguments[1:], 0); err != nil {
			return err
		}
		if _, err := serverconfig.Load(); err != nil {
			return err
		}
		fmt.Println("服务端配置有效")
		return nil
	case "set":
		return configSetCommand(arguments[1:])
	default:
		fmt.Fprint(os.Stderr, usage)
		return fmt.Errorf("未知命令 config %s", arguments[0])
	}
}

// configSetCommand 修改配置文件中的一项配置，配置文件不存在时新建，写入后报告配置是否完整。
func configSetCommand(arguments []string) error {
	flags := newFlags("config set", "<配置项> <值>")
	flags.Usage = func() {
		fmt.Fprintln(os.Stderr, "用法：config set <配置项> <值>")
		fmt.Fprintln(os.Stderr, "配置项：\n  "+strings.Join(serverconfig.Keys(), "\n  "))
	}
	if err := parseFlags(flags, arguments, 2); err != nil {
		return err
	}
	if err := serverconfig.SetValues([]serverconfig.Setting{{Key: flags.Arg(0), Value: flags.Arg(1)}}); err != nil {
		return err
	}
	fmt.Printf("已写入 %s，重启服务端后生效\n", serverconfig.Path())
	if _, err := serverconfig.Load(); err != nil {
		fmt.Printf("配置尚不完整：%v\n", err)
	}
	return nil
}
