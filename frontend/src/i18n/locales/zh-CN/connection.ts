/** 简体中文·连接文案。 */
const connection = {
  title: "连接服务器",
  description: "输入服务器地址。",
  serverUrlLabel: "服务器地址",
  serverUrlRequired: "请输入服务器地址。",
  serverUrlInvalid: "请输入完整有效的服务器地址。",
  detect: "检测",
  detecting: "正在检测…",
  connect: "连接",
  connecting: "正在连接…",
  connectionError: "无法连接到该服务器，请检查地址后重试。",
  savedServerUnreachable: "暂时无法连接到 {{host}}，请检查网络后重试。",
  serverNotInstalled: "{{host}} 尚未完成初始化，请先在浏览器中打开该地址完成安装。",
  serverOutdated: "{{host}} 的服务器版本过旧，请联系管理员升级服务器。",
  upgrade: {
    title: "需要升级客户端",
    description: "{{host}} 要求使用更新版本的客户端。",
    download: "下载新版本",
    downloading: "正在下载新版本",
    changeServer: "更换服务器",
  },
  update: {
    ready: "新版本 {{version}} 已就绪，重启后生效。",
    restart: "重启并更新",
    available: "新版本 {{version}} 可用，请从服务器下载页安装。",
    download: "前往下载",
  },
  clientLink: {
    title: "在客户端中使用",
    description: "客户端打开后自动填入当前服务器，确认即可连接。",
    desktop: "桌面端",
    desktopHelp: "已安装桌面端时直接打开，未安装时先下载。",
    openDesktop: "打开桌面端",
    downloadDesktop: "下载桌面端",
    mobile: "移动端",
    mobileHelp: "已安装移动端时，用手机相机扫码打开。",
    qrCodeAlt: "移动端连接二维码",
  },
}

export default connection
