(() => {
  const reduceMotion = matchMedia("(prefers-reduced-motion: reduce)")

  // 导航滚动离开顶部后显示背景。
  const nav = document.getElementById("lp-nav")
  const syncNav = () => nav.classList.toggle("is-scrolled", scrollY > 16)
  syncNav()
  addEventListener("scroll", syncNav, { passive: true })

  const toggle = document.getElementById("lp-nav-toggle")
  const menu = document.getElementById("lp-nav-menu")

  // 关闭窄屏菜单。
  const closeMenu = () => {
    menu.hidden = true
    toggle.setAttribute("aria-expanded", "false")
  }

  toggle.addEventListener("click", (event) => {
    const opening = menu.hidden
    menu.hidden = !opening
    toggle.setAttribute("aria-expanded", String(opening))
    event.stopPropagation()
  })
  document.addEventListener("click", (event) => {
    if (!menu.hidden && (!menu.contains(event.target) || event.target.closest("a"))) closeMenu()
  })
  document.addEventListener("keydown", (event) => {
    if (event.key !== "Escape" || menu.hidden) return
    closeMenu()
    toggle.focus()
  })
  matchMedia("(min-width: 801px)").addEventListener("change", closeMenu)

  // 标出访问者当前设备对应的平台，下载页将其排在最前。
  const agent = navigator.userAgent
  const touchMac = /Macintosh/.test(agent) && navigator.maxTouchPoints > 1
  const platform = /iPhone|iPad|iPod/.test(agent) || touchMac ? "ios" : /Android/.test(agent) ? "android" : /Windows/.test(agent) ? "windows" : /Macintosh/.test(agent) ? "macos" : /Linux|X11/.test(agent) ? "linux" : ""
  const card = platform && document.querySelector('[data-platform="' + platform + '"]')
  if (card) card.dataset.current = ""

  // 区块进入视口时淡入，不支持或偏好减弱动效时直接显示。
  if ("IntersectionObserver" in window && !reduceMotion.matches) {
    document.documentElement.classList.add("lp-motion")
    const observer = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue
        entry.target.classList.add("is-in")
        observer.unobserve(entry.target)
      }
    }, { rootMargin: "0px 0px -12% 0px", threshold: 0.08 })
    document.querySelectorAll("[data-reveal]").forEach((node) => observer.observe(node))
  }

  // 文档页：窄屏下侧栏滑入与收起。
  const docsMenu = document.querySelector("[data-docs-menu]")
  if (docsMenu) {
    const root = document.documentElement
    const setDocsMenu = (open) => {
      root.classList.toggle("lp-docs-menu-open", open)
      docsMenu.setAttribute("aria-expanded", String(open))
    }
    docsMenu.addEventListener("click", () => setDocsMenu(!root.classList.contains("lp-docs-menu-open")))
    document.querySelector("[data-docs-menu-close]").addEventListener("click", () => setDocsMenu(false))
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape") setDocsMenu(false)
    })
    matchMedia("(min-width: 801px)").addEventListener("change", () => setDocsMenu(false))
  }

  // 文档页：翻页时保留侧栏的展开分组与滚动位置；当前页面不在可视范围时，把它滚到侧栏中部。
  const sidebar = document.getElementById("lp-docs-sidebar")
  if (sidebar) {
    const key = "luway.docs.sidebar." + sidebar.dataset.section
    const groups = [...sidebar.querySelectorAll("details")]
    let saved = null
    try { saved = JSON.parse(sessionStorage.getItem(key) || "null") } catch {}
    if (saved && Array.isArray(saved.open) && typeof saved.scroll === "number") {
      groups.forEach((group, index) => { if (saved.open.includes(index)) group.open = true })
      sidebar.scrollTop = saved.scroll
    }
    const current = sidebar.querySelector('.lp-docs-nav [aria-current="page"]')
    if (current) {
      const item = current.getBoundingClientRect()
      const box = sidebar.getBoundingClientRect()
      if (item.top < box.top || item.bottom > box.bottom) sidebar.scrollTop += item.top - box.top - box.height / 2
    }
    const save = () => {
      const open = groups.flatMap((group, index) => (group.open ? [index] : []))
      try { sessionStorage.setItem(key, JSON.stringify({ scroll: sidebar.scrollTop, open })) } catch {}
    }
    sidebar.addEventListener("click", (event) => {
      if (event.target.closest("a")) save()
    })
    addEventListener("pagehide", save)
  }
  // 带 data-progress 的元素获得 --p：从窗口底部升起时为 0，完全进入视野后为 1，叠放的窗口随之滑开；减弱动效时保持样式中的 1。
  const progressed = reduceMotion.matches ? [] : [...document.querySelectorAll("[data-progress]")]
  if (progressed.length) {
    const update = () => {
      for (const node of progressed) {
        const top = node.getBoundingClientRect().top
        const progress = Math.min(1, Math.max(0, (innerHeight - top) / (innerHeight * 0.75)))
        node.style.setProperty("--p", progress.toFixed(3))
      }
    }
    let queued = false
    const queue = () => {
      if (queued) return
      queued = true
      requestAnimationFrame(() => {
        queued = false
        update()
      })
    }
    addEventListener("scroll", queue, { passive: true })
    addEventListener("resize", queue)
    update()
  }

  // 页脚订阅与授权管理：先做前端占位，不发起真实请求。
  const newsletter = document.getElementById("lp-newsletter")
  if (newsletter) {
    const status = document.getElementById("lp-newsletter-status")
    newsletter.addEventListener("submit", (event) => {
      event.preventDefault()
      if (!newsletter.checkValidity()) {
        newsletter.reportValidity()
        return
      }
      status.textContent = status.dataset.subscribing || ""
      status.className = "lp-newsletter-status"
      window.setTimeout(() => {
        status.textContent = status.dataset.failed || ""
        status.className = "lp-newsletter-status is-error"
      }, 400)
    })
  }
  const licenseForm = document.getElementById("lp-license-form")
  if (licenseForm) {
    const status = document.getElementById("lp-license-status")
    licenseForm.addEventListener("submit", (event) => {
      event.preventDefault()
      if (!licenseForm.checkValidity()) {
        licenseForm.reportValidity()
        return
      }
      status.hidden = false
      status.textContent = status.dataset.placeholder || ""
      status.className = "lp-form-status is-success"
    })
  }

  const canvas = document.querySelector(".lp-stars")
  const context = canvas && canvas.getContext && canvas.getContext("2d")
  if (!context) return
  let stars = []
  let visible = false

  // 绘制一帧星点，部分星点随时间闪烁。
  const draw = (time) => {
    context.clearRect(0, 0, canvas.offsetWidth, canvas.offsetHeight)
    context.fillStyle = "#fff"
    stars.forEach((star, index) => {
      context.globalAlpha = star.speed ? star.alpha * (0.55 + 0.45 * Math.sin(time / 1000 * star.speed + index)) : star.alpha
      context.beginPath()
      context.arc(star.x, star.y, star.radius, 0, Math.PI * 2)
      context.fill()
    })
  }

  // 按画布尺寸重新生成星点并绘制静态画面。
  const seed = () => {
    const ratio = Math.min(devicePixelRatio || 1, 2)
    const width = canvas.offsetWidth
    const height = canvas.offsetHeight
    canvas.width = Math.round(width * ratio)
    canvas.height = Math.round(height * ratio)
    context.setTransform(ratio, 0, 0, ratio, 0, 0)
    stars = Array.from({ length: Math.round(width * height / 5200) }, () => ({
      x: Math.random() * width,
      y: Math.random() * height,
      radius: Math.random() < 0.08 ? 1.3 : 0.4 + Math.random() * 0.6,
      alpha: 0.15 + Math.random() * 0.55,
      speed: Math.random() < 0.25 ? 0.6 + Math.random() * 1.6 : 0,
    }))
    draw(0)
  }

  // 画布可见时逐帧重绘。
  const loop = (time) => {
    if (!visible) return
    draw(time)
    requestAnimationFrame(loop)
  }

  seed()
  addEventListener("resize", seed)
  if ("IntersectionObserver" in window && !reduceMotion.matches) {
    new IntersectionObserver((entries) => {
      const wasVisible = visible
      visible = entries[0].isIntersecting
      if (visible && !wasVisible) requestAnimationFrame(loop)
    }).observe(canvas)
  }
})()
