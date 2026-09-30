package toolchain

import "runtime"

const (
	// uvVersion 是首次安装的 uv 版本，已安装版本低于它时自动安装。
	uvVersion = "0.12.19"
	// nodeVersion 是首次安装的 Node.js 版本，已安装版本低于它时自动安装。
	nodeVersion = "24.21.0"
	// PythonVersion 是首次安装时以 uv 安装为默认解释器的 Python 版本。
	PythonVersion = "3.13.15"
	// defaultPyPIIndexURL 是官方 PyPI 简单索引，uv 从中按 wheel 文件名查找下载地址。
	defaultPyPIIndexURL = "https://pypi.org/simple"
	// pythonSeries 是默认 Python 的版本系列，更新时升级到该系列的最新补丁版本。
	pythonSeries = "3.13"
	// defaultNodeDownloadURL 是 Node.js 发行物的官方下载前缀，完整地址为 <前缀>/v<版本>/<文件名>。
	defaultNodeDownloadURL = "https://nodejs.org/dist"
)

// regionTraceURL 返回访问方公网出口所在国家代码（loc=XX）的地址。
const regionTraceURL = "https://www.cloudflare.com/cdn-cgi/trace"

// chinaSources 是中国大陆网络使用的镜像。
var chinaSources = Sources{
	NodeDownloadURL:     "https://npmmirror.com/mirrors/node",
	PythonInstallMirror: "https://registry.npmmirror.com/-/binary/python-build-standalone",
	PyPIIndexURL:        "https://pypi.tuna.tsinghua.edu.cn/simple",
	NPMRegistry:         "https://registry.npmmirror.com",
}

// artifact 是一个平台的发行物文件名与 SHA256。
type artifact struct {
	file   string
	sha256 string
}

// uvArtifacts 按 GOOS/GOARCH 列出 uv 在 PyPI 上的 wheel。
var uvArtifacts = map[string]artifact{
	"darwin/arm64":  {file: "uv-0.12.19-py3-none-macosx_11_0_arm64.whl", sha256: "5da0401c0898b5fe767968f5a72f27525276b119d69e0c7c25b721f63ecef650"},
	"darwin/amd64":  {file: "uv-0.12.19-py3-none-macosx_10_12_x86_64.whl", sha256: "13e1f008a379b71f07a2f8caf62c0baac9d9039f4238e2e9192f7ddcfc0a1913"},
	"windows/amd64": {file: "uv-0.12.19-py3-none-win_amd64.whl", sha256: "dcbc531a96762569bbfe9639b4f45f00aabff51f427540711f63e7c23f225fdf"},
	"windows/arm64": {file: "uv-0.12.19-py3-none-win_arm64.whl", sha256: "76b48a93e5c9e38cf3b41dcb02f9170935dcb9cd2f99aa0dd44caaa2d8dc2b9d"},
	"linux/amd64":   {file: "uv-0.12.19-py3-none-manylinux_2_17_x86_64.manylinux2014_x86_64.whl", sha256: "a63d18a0aa38ee9f21a5406afbbaeb41303bcd954be9d6b7c1b95ac275e53958"},
	"linux/arm64":   {file: "uv-0.12.19-py3-none-manylinux_2_17_aarch64.manylinux2014_aarch64.musllinux_1_1_aarch64.whl", sha256: "b466eb0f74645883df52446d54474905e8515d75fdf0b313bf099dedc3237896"},
}

// nodeArtifacts 按 GOOS/GOARCH 列出 Node.js 发行物。
var nodeArtifacts = map[string]artifact{
	"darwin/arm64":  {file: "node-v24.21.0-darwin-arm64.tar.gz", sha256: "bed7eea5325e1108f32ce5228ddd6a5f0f08a499ee42aa7442aea583702f6057"},
	"darwin/amd64":  {file: "node-v24.21.0-darwin-x64.tar.gz", sha256: "1462cb3b3046b815cf8ea436d3da450ec1a9f11dac7e5a46b0ada5305d7e8097"},
	"windows/amd64": {file: "node-v24.21.0-win-x64.zip", sha256: "158f7685b44de51f6c0df1d153526cbcd3e1bc739a8dfc607721cef75de9e541"},
	"windows/arm64": {file: "node-v24.21.0-win-arm64.zip", sha256: "8779b1bde1d39f8d420e3b57aa657b39891af434d3de44a919044cec06785921"},
	"linux/amd64":   {file: "node-v24.21.0-linux-x64.tar.gz", sha256: "6e1db87ef58b8819e5d5402eff1536491b18edd8eb7bee5ef7897876e88dc5ff"},
	"linux/arm64":   {file: "node-v24.21.0-linux-arm64.tar.gz", sha256: "724282c3b43aec998aa9527380465b45d229e021b58035f5f4f63095eabfe5d5"},
}

// uvWheelContent 返回指定版本的 uv wheel 中存放可执行文件的目录。
func uvWheelContent(version string) string {
	return "uv-" + version + ".data/scripts"
}

// platform 返回当前平台在发行物清单中的键。
func platform() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
