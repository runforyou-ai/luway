package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// main 检查 Android 开发依赖并输出配置建议。
func main() {
	fmt.Println("Checking Android development dependencies...")
	fmt.Println()

	errors := []string{}

	if !checkCommand("go", "version") {
		errors = append(errors, "Go is not installed. Install from https://go.dev/dl/")
	} else {
		fmt.Println("✓ Go is installed")
	}

	androidHome := os.Getenv("ANDROID_HOME")
	if androidHome == "" {
		androidHome = os.Getenv("ANDROID_SDK_ROOT")
	}
	if androidHome == "" {
		home, _ := os.UserHomeDir()
		possiblePaths := []string{
			filepath.Join(home, "Android", "Sdk"),
			filepath.Join(home, "Library", "Android", "sdk"),
			"/usr/local/share/android-sdk",
		}
		for _, p := range possiblePaths {
			if _, err := os.Stat(p); err == nil {
				androidHome = p
				break
			}
		}
	}

	if androidHome == "" {
		errors = append(errors, "ANDROID_HOME not set. Install Android Studio and set ANDROID_HOME environment variable")
	} else {
		fmt.Printf("✓ ANDROID_HOME: %s\n", androidHome)
	}

	if !checkCommand("adb", "version") {
		if androidHome != "" {
			platformTools := filepath.Join(androidHome, "platform-tools")
			errors = append(errors, fmt.Sprintf("adb not found. Add %s to PATH", platformTools))
		} else {
			errors = append(errors, "adb not found. Install Android SDK Platform-Tools")
		}
	} else {
		fmt.Println("✓ adb is installed")
	}

	if !checkCommand("emulator", "-list-avds") {
		if androidHome != "" {
			emulatorPath := filepath.Join(androidHome, "emulator")
			errors = append(errors, fmt.Sprintf("emulator not found. Add %s to PATH", emulatorPath))
		} else {
			errors = append(errors, "emulator not found. Install Android Emulator via SDK Manager")
		}
	} else {
		fmt.Println("✓ Android Emulator is installed")
	}

	ndkHome := os.Getenv("ANDROID_NDK_HOME")
	if ndkHome == "" && androidHome != "" {
		ndkDir := filepath.Join(androidHome, "ndk")
		if entries, err := os.ReadDir(ndkDir); err == nil {
			for _, entry := range entries {
				if entry.IsDir() {
					ndkHome = filepath.Join(ndkDir, entry.Name())
					break
				}
			}
		}
	}

	if ndkHome == "" {
		errors = append(errors, "Android NDK not found. Install NDK via Android Studio > SDK Manager > SDK Tools > NDK (Side by side)")
	} else {
		fmt.Printf("✓ Android NDK: %s\n", ndkHome)
	}

	if !checkCommand("java", "-version") {
		errors = append(errors, "Java not found. Install JDK 11+ (OpenJDK recommended)")
	} else {
		fmt.Println("✓ Java is installed")
	}

	if checkCommand("emulator", "-list-avds") {
		cmd := exec.Command("emulator", "-list-avds")
		output, err := cmd.Output()
		if err == nil && len(strings.TrimSpace(string(output))) > 0 {
			avds := strings.Split(strings.TrimSpace(string(output)), "\n")
			fmt.Printf("✓ Found %d Android Virtual Device(s)\n", len(avds))
		} else {
			offerCreateAVD(androidHome)
		}
	}

	fmt.Println()

	if len(errors) > 0 {
		fmt.Println("❌ Missing dependencies:")
		for _, err := range errors {
			fmt.Printf("   - %s\n", err)
		}
		fmt.Println()
		fmt.Println("Setup instructions:")
		fmt.Println("1. Install Android Studio: https://developer.android.com/studio")
		fmt.Println("2. Open SDK Manager and install:")
		fmt.Println("   - Android SDK Platform (API 35)")
		fmt.Println("   - Android SDK Build-Tools")
		fmt.Println("   - Android SDK Platform-Tools")
		fmt.Println("   - Android Emulator")
		fmt.Println("   - NDK (Side by side)")
		fmt.Println("3. Set environment variables:")
		if runtime.GOOS == "darwin" {
			fmt.Println("   export ANDROID_HOME=$HOME/Library/Android/sdk")
		} else {
			fmt.Println("   export ANDROID_HOME=$HOME/Android/Sdk")
		}
		fmt.Println("   export PATH=$PATH:$ANDROID_HOME/platform-tools:$ANDROID_HOME/emulator")
		fmt.Println("4. Create an AVD via Android Studio > Tools > Device Manager")
		os.Exit(1)
	}

	fmt.Println("✓ All Android development dependencies are installed!")
}

// checkCommand 判断指定命令是否能够成功执行。
func checkCommand(name string, args ...string) bool {
	cmd := exec.Command(name, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

// offerCreateAVD 使用已安装的系统镜像引导用户创建 Android 虚拟设备。
func offerCreateAVD(androidHome string) {
	abi := "x86_64"
	if runtime.GOARCH == "arm64" {
		abi = "arm64-v8a"
	}

	// 按 API 版本选择与主机架构匹配的最新系统镜像。
	var img string
	if androidHome != "" {
		matches, _ := filepath.Glob(filepath.Join(androidHome, "system-images", "android-*", "*", abi))
		bestAPI := -1
		for _, m := range matches {
			apiDir := filepath.Base(filepath.Dir(filepath.Dir(m)))
			api, err := strconv.Atoi(strings.TrimPrefix(apiDir, "android-"))
			if err != nil {
				continue
			}
			if api > bestAPI {
				bestAPI = api
				img = m
			}
		}
	}

	avdmanager := findAVDManager(androidHome)

	if img == "" || avdmanager == "" {
		fmt.Println("⚠ No Android Virtual Devices found.")
		fmt.Println("   Install a system image and create an AVD, e.g.:")
		fmt.Printf("     sdkmanager 'system-images;android-35;google_apis;%s'\n", abi)
		fmt.Printf("     avdmanager create avd --name wails --package 'system-images;android-35;google_apis;%s' --device pixel_7\n", abi)
		return
	}

	// 将系统镜像目录转换为 avdmanager 使用的软件包路径。
	rel := strings.TrimPrefix(img, filepath.Join(androidHome, "system-images")+string(os.PathSeparator))
	pkg := "system-images;" + strings.ReplaceAll(rel, string(os.PathSeparator), ";")

	fmt.Println("⚠ No Android Virtual Devices found.")
	fmt.Printf("   Would you like to create a 'wails' AVD from %s?\n", pkg)
	if !promptUser("Create AVD?") {
		fmt.Println("   Skipping AVD creation.")
		fmt.Printf("   Create manually: avdmanager create avd --name wails --package '%s' --device pixel_7\n", pkg)
		return
	}

	cmd := exec.Command(avdmanager, "create", "avd", "--name", "wails", "--package", pkg, "--device", "pixel_7", "--force")
	cmd.Stdin = strings.NewReader("no\n")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Printf("   Failed to create AVD: %v\n", err)
	} else {
		fmt.Println("   ✅ 'wails' AVD created")
	}
}

// findAVDManager 查找 PATH 或 Android SDK 中的 avdmanager。
func findAVDManager(androidHome string) string {
	if p, err := exec.LookPath("avdmanager"); err == nil {
		return p
	}
	if androidHome != "" {
		matches, _ := filepath.Glob(filepath.Join(androidHome, "cmdline-tools", "*", "bin", "avdmanager"))
		// 优先使用 latest，其余目录按数值版本选择。
		best := ""
		bestVersion := -1.0
		for _, m := range matches {
			version := filepath.Base(filepath.Dir(filepath.Dir(m)))
			if version == "latest" {
				return m
			}
			v, err := strconv.ParseFloat(version, 64)
			if err != nil {
				v = 0
			}
			if v > bestVersion {
				bestVersion = v
				best = m
			}
		}
		return best
	}
	return ""
}

// promptUser 读取用户确认，并支持 CI 自动确认。
func promptUser(question string) bool {
	if os.Getenv("CI") != "" || os.Getenv("TASK_FORCE_YES") == "true" {
		fmt.Printf("%s [y/N]: y (auto-accepted)\n", question)
		return true
	}
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("%s [y/N]: ", question)
	response, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	response = strings.ToLower(strings.TrimSpace(response))
	return response == "y" || response == "yes"
}
