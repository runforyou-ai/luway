// wailsreference 在工作区缓存中生成独立的官方升级参考文件。
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// main 生成完整 React 脚手架和带当前产品元数据的官方构建资源。
func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// generate 为每次对比创建独立目录并记录所用 CLI 版本。
func generate() error {
	if len(os.Args) != 2 || filepath.Base(os.Args[1]) != os.Args[1] || strings.ContainsAny(os.Args[1], `/\`) || os.Args[1] == "." || os.Args[1] == ".." {
		return fmt.Errorf("用法：wailsreference <应用文件名>")
	}
	// wails3 version 把版本号写到标准错误。
	version, err := exec.Command("wails3", "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("读取 Wails CLI 版本：%w", err)
	}
	root, err := filepath.Abs(".task")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(root, "wails-reference-")
	if err != nil {
		return err
	}
	config, err := filepath.Abs("build/config.yml")
	if err != nil {
		return err
	}
	commands := [][]string{
		{"init", "-n", os.Args[1], "-t", "react", "-d", filepath.Join(dir, "scaffold"), "-skipgomodtidy"},
		{"update", "build-assets", "-name", os.Args[1], "-binaryname", os.Args[1], "-config", config, "-dir", filepath.Join(dir, "build-assets")},
	}
	for _, args := range commands {
		command := exec.Command("wails3", args...)
		command.Stdout, command.Stderr = os.Stdout, os.Stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("生成官方参考文件：%w", err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "wails-version.txt"), []byte(strings.TrimSpace(string(version))+"\n"), 0644); err != nil {
		return err
	}
	fmt.Printf("官方参考目录：%s\n请对比 scaffold 中的完整项目和 build-assets 中的构建资源，再人工合并到工作区。\n", dir)
	return nil
}
