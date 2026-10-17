/** 简体中文·平台后台文案。 */
const admin = {
  accountLoadError: "读取平台管理员账号失败，请稍后重试。",
  shell: {
    navLabel: "平台后台导航",
    logout: "退出后台",
    changePassword: "修改密码",
    changePasswordDescription: "修改后其他登录会话会立即失效，当前后台会话保持登录。",
    collapseSidebar: "收起侧栏",
    expandSidebar: "展开侧栏",
    breadcrumbLabel: "当前位置",
    accountMenu: "账号菜单",
  },
  nav: {
    groups: {
      operations: "运营",
      monitor: "监控",
      system: "系统",
    },
    dashboard: "仪表板",
    workspaces: "工作区管理",
    accounts: "用户管理",
    overview: {
      usage: "业务使用",
      runtime: "运行状态",
      errors: "近期错误",
    },
    system: {
      site: "站点设置",
      storage: "文件存储",
      email: "邮件发送",
      wechat: "微信开放平台",
    },
    license: "授权",
  },
  login: {
    title: "平台后台登录",
    description: "使用平台管理员账号登录 {{product}} 管理后台。",
    emailLabel: "邮箱",
    passwordLabel: "密码",
    showPassword: "显示密码",
    hidePassword: "隐藏密码",
    submit: "登录",
    submitting: "正在登录…",
  },
  support: {
    banner: "你正在以平台支援身份访问此工作区。操作会被审计。",
    exit: "退出支援",
  },
}

export default admin
