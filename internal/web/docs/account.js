(() => {
  // 按 Web 应用保存的登录令牌刷新页头入口：令牌有效时显示当前账号，否则显示登录。
  const link = document.querySelector("[data-account-link]")
  if (!link) return
  const signIn = link.textContent
  let generation = 0
  const showSignIn = () => {
    link.replaceChildren(signIn)
    link.classList.remove("site-account")
    link.removeAttribute("title")
    link.removeAttribute("aria-label")
  }
  const showAccount = (name) => {
    const avatar = document.createElement("span")
    avatar.className = "site-avatar"
    avatar.textContent = Array.from(name)[0].toUpperCase()
    const label = document.createElement("span")
    label.className = "site-account-name"
    label.textContent = name
    link.replaceChildren(avatar, label)
    link.classList.add("site-account")
    link.title = link.dataset.accountLink
    link.setAttribute("aria-label", link.dataset.accountLink + " · " + name)
  }
  const refresh = () => {
    const current = ++generation
    let stored = null
    try { stored = JSON.parse(localStorage.getItem("app.token") || "null") } catch {}
    if (!stored || !stored.token || !(Date.parse(stored.expiresAt) > Date.now())) {
      delete link.dataset.pending
      showSignIn()
      return
    }
    link.dataset.pending = ""
    fetch("/api/account", { headers: { Authorization: "Bearer " + stored.token } })
      .then((response) => (response.ok ? response.json() : null))
      .catch(() => null)
      .then((account) => {
        // 等待期间令牌再次变化时丢弃本次结果。
        if (current !== generation) return
        const name = account && (account.displayName || account.email)
        name ? showAccount(name) : showSignIn()
        delete link.dataset.pending
      })
  }
  refresh()
  addEventListener("storage", (event) => { if (event.key === "app.token" || event.key === null) refresh() })
  addEventListener("pageshow", (event) => { if (event.persisted) refresh() })
})()
