// 网站渠道嵌入脚本：在宿主页显示聊天挂件。
(function () {
  var script = document.currentScript;
  if (!script) {
    return;
  }

  var baseUrl;
  var channelId = "";
  var preview = false;
  var scriptUrl = new URL(script.src);
  baseUrl = scriptUrl.origin;
  channelId = (scriptUrl.searchParams.get("id") || "").trim();
  preview = scriptUrl.searchParams.get("preview") === "1";
  if (
    !preview &&
    !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(
      channelId,
    )
  ) {
    return;
  }

  var rootId = preview
    ? "messenger-widget-preview"
    : "messenger-widget-" + channelId.toLowerCase();
  if (document.getElementById(rootId)) {
    return;
  }

  var root = document.createElement("div");
  root.id = rootId;
  var shadow = root.attachShadow({ mode: "open" });
  var mobileQuery = window.matchMedia("(max-width: 640px)");
  var bottomInset = 0;
  var expanded = false;
  var frameReady = false;
  var previewConfig = null;
  var previewParentOrigin = "";
  // 服务端按品牌注入的宿主页全局对象名、设置对象名和打开挂件的属性名。
  var sdk = /*CV_SDK*/ null;
  // 宿主传入的签名身份；退出后下一次加载聊天页时轮换匿名访客。
  var settings = window[sdk.settings] || {};
  var customerToken =
    typeof settings.customerToken === "string" ? settings.customerToken : "";
  var rotateVisitor = false;
  var identityExpiredListeners = [];
  var pageTimer = 0;
  var desktopPanelWidth = 400;
  var desktopPanelHeight = 640;
  var expandedPanelMaxWidth = 480;
  var expandedPanelMaxHeight = 720;
  // 服务端按访客语言偏好注入的挂件文案。
  var widgetCopy = /*CV_COPY*/ null;
  var hostScrollLock = { applied: false, bodyOverflow: "", htmlOverflow: "" };

  var style = document.createElement("style");
  style.textContent = [
    ":host{all:initial}",
    "/*CV_THEME*/",
    ".cv-panel{box-sizing:border-box;position:fixed;z-index:2147483000;width:400px;height:640px;right:24px;bottom:96px;max-width:calc(100vw - 24px);max-height:calc(100dvh - 144px);overflow:hidden;border:1px solid rgba(24,24,27,.10);border-radius:24px;background:#fff;box-shadow:0 24px 72px rgba(15,23,42,.22);opacity:0;visibility:hidden;pointer-events:none;transform:translateY(8px) scale(.985);transform-origin:bottom right;transition:opacity .18s ease-out,transform .18s ease-out,visibility .18s}",
    '.cv-panel[data-open="true"]{opacity:1;visibility:visible;pointer-events:auto;transform:none}',
    ".cv-frame{display:block;width:100%;height:100%;border:0;background:#fff}",
    ".cv-button{box-sizing:border-box;position:fixed;right:24px;bottom:calc(24px + env(safe-area-inset-bottom,0px));z-index:2147483001;width:56px;height:56px;border:1px solid rgba(255,255,255,.16);border-radius:18px;background:var(--cv-theme);color:var(--cv-on-theme);box-shadow:var(--cv-launcher-shadow);cursor:pointer;display:inline-flex;align-items:center;justify-content:center;padding:0;transition:transform .18s ease-out,box-shadow .18s ease-out,border-radius .18s ease-out}",
    ".cv-button:hover{transform:translateY(-2px)}",
    '.cv-button[data-open="true"]{border-radius:999px}',
    ".cv-button:focus-visible{outline:3px solid var(--cv-focus);outline-offset:3px}",
    ".cv-icon{display:block;width:22px;height:22px;fill:none;stroke:currentColor;stroke-width:2;stroke-linecap:round;stroke-linejoin:round}",
    ".cv-icon-close{display:none}",
    '.cv-button[data-open="true"] .cv-icon-chat{display:none}',
    '.cv-button[data-open="true"] .cv-icon-close{display:block}',
    ".cv-badge{position:absolute;top:-3px;right:-3px;width:12px;height:12px;border:2px solid #fff;border-radius:999px;background:#dc2626;box-shadow:0 2px 8px rgba(15,23,42,.2)}",
    ".cv-badge[hidden]{display:none}",
    "@media (prefers-reduced-motion:reduce){.cv-button,.cv-panel{transition:none}.cv-button:hover,.cv-button:active{transform:none}.cv-panel{transform:none}}",
  ].join("");

  var panel = document.createElement("div");
  panel.className = "cv-panel";
  panel.setAttribute("role", "dialog");
  panel.setAttribute("aria-label", widgetCopy.dialog);
  panel.setAttribute("aria-hidden", "true");

  var frame = document.createElement("iframe");
  frame.className = "cv-frame";
  frame.title = widgetCopy.dialog;
  frame.loading = preview ? "eager" : "lazy";
  frame.referrerPolicy = "strict-origin-when-cross-origin";
  frame.allow = "clipboard-write";
  frame.src = preview
    ? baseUrl + "/embed/preview/frame"
    : baseUrl + "/embed/widget/" + encodeURIComponent(channelId);
  panel.appendChild(frame);

  var button = document.createElement("button");
  button.className = "cv-button";
  button.type = "button";
  button.setAttribute("aria-expanded", "false");
  button.setAttribute("aria-label", widgetCopy.open);
  button.appendChild(
    createIcon("cv-icon-chat", [
      [
        "path",
        {
          d: "M21 12a8.6 8.6 0 0 1-9 8.5 9.8 9.8 0 0 1-3.8-.8L3 21l1.4-4.7A8.2 8.2 0 0 1 3 12a8.6 8.6 0 0 1 9-8.5A8.6 8.6 0 0 1 21 12Z",
        },
      ],
      ["path", { d: "M8.5 11.5h7" }],
      ["path", { d: "M8.5 14.5H13" }],
    ]),
  );
  var unreadBadge = document.createElement("span");
  unreadBadge.className = "cv-badge";
  unreadBadge.hidden = true;
  unreadBadge.setAttribute("aria-hidden", "true");
  button.appendChild(unreadBadge);
  button.appendChild(
    createIcon("cv-icon-close", [
      ["path", { d: "M18 6 6 18" }],
      ["path", { d: "m6 6 12 12" }],
    ]),
  );

  function createIcon(className, nodes) {
    var svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
    svg.setAttribute("class", "cv-icon " + className);
    svg.setAttribute("viewBox", "0 0 24 24");
    svg.setAttribute("aria-hidden", "true");
    svg.setAttribute("focusable", "false");
    nodes.forEach(function (node) {
      var el = document.createElementNS("http://www.w3.org/2000/svg", node[0]);
      Object.keys(node[1]).forEach(function (key) {
        el.setAttribute(key, node[1][key]);
      });
      svg.appendChild(el);
    });
    return svg;
  }

  // 管理端预览固定使用桌面布局，公开挂件按宿主页视口响应。
  function isMobile() {
    return !preview && mobileQuery.matches;
  }

  // 按浏览器可用宽高分别限制桌面面板尺寸。
  function responsiveDesktopPanelSize(maxWidth, maxHeight) {
    var availableWidth = Math.max(0, window.innerWidth - 48);
    var availableHeight = Math.max(0, window.innerHeight - 144);
    return {
      width: Math.min(maxWidth, availableWidth) + "px",
      height: Math.min(maxHeight, availableHeight) + "px",
    };
  }

  function relativeLuminance(hexColor) {
    var channels = hexColor.slice(1).match(/.{2}/g).map(function (channel) {
      var value = Number.parseInt(channel, 16) / 255;
      return value <= 0.04045
        ? value / 12.92
        : Math.pow((value + 0.055) / 1.055, 2.4);
    });
    return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
  }

  function applyPreviewTheme(value) {
    var color =
      typeof value.themeColor === "string"
        ? value.themeColor.trim().toUpperCase()
        : "";
    if (!/^#[0-9A-F]{6}$/.test(color)) {
      return;
    }
    var luminance = relativeLuminance(color);
    var whiteContrast = 1.05 / (luminance + 0.05);
    var darkContrast =
      (luminance + 0.05) / (relativeLuminance("#1C1917") + 0.05);
    var focus = "color-mix(in srgb, " + color + " 40%, transparent)";
    var shadow =
      "0 10px 28px color-mix(in srgb, " + color + " 42%, transparent)";
    if (whiteContrast < 3) {
      focus = "rgba(28, 25, 23, 0.35)";
      shadow = "0 8px 24px rgba(28, 25, 23, 0.12)";
    }
    root.style.setProperty("--cv-theme", color);
    root.style.setProperty(
      "--cv-on-theme",
      whiteContrast >= darkContrast ? "#FFFFFF" : "#1C1917",
    );
    root.style.setProperty("--cv-focus", focus);
    root.style.setProperty("--cv-launcher-shadow", shadow);
  }

  function sendPreviewConfig() {
    if (!preview || !previewConfig || !frame.contentWindow) {
      return;
    }
    frame.contentWindow.postMessage(previewConfig, baseUrl);
  }

  function notifyPreviewReady() {
    if (!preview || window.parent === window) {
      return;
    }
    window.parent.postMessage(
      { type: "messenger:preview-ready" },
      previewParentOrigin || "*",
    );
  }

  function syncHostScrollLock() {
    var shouldLock = panel.dataset.open === "true" && isMobile();
    if (shouldLock === hostScrollLock.applied) {
      return;
    }
    if (shouldLock) {
      hostScrollLock.bodyOverflow = document.body.style.overflow;
      hostScrollLock.htmlOverflow = document.documentElement.style.overflow;
      document.body.style.overflow = "hidden";
      document.documentElement.style.overflow = "hidden";
      hostScrollLock.applied = true;
      return;
    }
    document.body.style.overflow = hostScrollLock.bodyOverflow;
    document.documentElement.style.overflow = hostScrollLock.htmlOverflow;
    hostScrollLock.applied = false;
  }

  function applyLayout() {
    var open = panel.dataset.open === "true";
    if (isMobile()) {
      expanded = false;
      // 手机上面板贴合可视区域，输入法弹出时随可视区域缩小并跟随其偏移。
      var viewport = window.visualViewport;
      panel.style.left = "0";
      panel.style.right = "0";
      panel.style.top = viewport ? viewport.offsetTop + "px" : "0";
      panel.style.bottom = viewport ? "auto" : "0";
      panel.style.width = "100vw";
      panel.style.height = viewport ? viewport.height + "px" : "100dvh";
      panel.style.maxWidth = "none";
      panel.style.maxHeight = "none";
      panel.style.borderRadius = "0";
      panel.style.border = "0";
      panel.style.boxShadow = "none";
      frame.style.height =
        bottomInset > 0 ? "calc(100% - " + bottomInset + "px)" : "";
      button.style.display = open && frameReady ? "none" : "inline-flex";
    } else if (expanded) {
      // 固定面板右下角，宽高按各自可用空间向左和上方增长。
      var expandedSize = responsiveDesktopPanelSize(
        expandedPanelMaxWidth,
        expandedPanelMaxHeight,
      );
      panel.style.left = "";
      panel.style.right = "24px";
      panel.style.top = "";
      panel.style.bottom = "96px";
      panel.style.width = expandedSize.width;
      panel.style.height = expandedSize.height;
      panel.style.maxWidth = "none";
      panel.style.maxHeight = "none";
      panel.style.borderRadius = "24px";
      panel.style.border = "1px solid rgba(24,24,27,.10)";
      panel.style.boxShadow = "0 28px 90px rgba(15,23,42,.24)";
      frame.style.height = "";
      button.style.display = "inline-flex";
    } else {
      var defaultSize = responsiveDesktopPanelSize(
        desktopPanelWidth,
        desktopPanelHeight,
      );
      panel.style.left = "";
      panel.style.right = "";
      panel.style.top = "";
      panel.style.bottom = "";
      panel.style.width = defaultSize.width;
      panel.style.height = defaultSize.height;
      panel.style.maxWidth = "none";
      panel.style.maxHeight = "none";
      panel.style.borderRadius = "";
      panel.style.border = "";
      panel.style.boxShadow = "";
      frame.style.height = "";
      button.style.display = "inline-flex";
    }
    syncHostScrollLock();
  }

  function setOpen(next) {
    if (!next) {
      expanded = false;
    }
    panel.dataset.open = next ? "true" : "false";
    button.dataset.open = next ? "true" : "false";
    panel.setAttribute("aria-hidden", next ? "false" : "true");
    button.setAttribute("aria-expanded", next ? "true" : "false");
    button.setAttribute("aria-label", next ? widgetCopy.close : widgetCopy.open);
    applyLayout();
    syncFrameState();
    if (next) {
      sendPage();
      frame.focus();
    } else {
      button.focus();
    }
  }

  function setBottomInset(px) {
    var next = Math.max(0, Math.min(2000, Math.round(Number(px) || 0)));
    bottomInset = next;
    applyLayout();
  }

  function handleViewportModeChange() {
    applyLayout();
    syncFrameState();
  }

  // 向聊天页下发当前签名身份，每次加载聊天页只下发一次。
  function sendIdentity() {
    if (preview || !frame.contentWindow) {
      return;
    }
    frame.contentWindow.postMessage(
      { type: "messenger:identity", customerToken: customerToken, rotate: rotateVisitor },
      baseUrl,
    );
    rotateVisitor = false;
  }

  // 向聊天页下发宿主页面地址、标题与来源页。
  function sendPage() {
    window.clearTimeout(pageTimer);
    pageTimer = 0;
    if (preview || !frame.contentWindow) {
      return;
    }
    frame.contentWindow.postMessage(
      {
        type: "messenger:page",
        url: window.location.href,
        title: document.title,
        referrer: document.referrer,
      },
      baseUrl,
    );
  }

  // 单页应用切换路由后等待标题更新再下发页面。
  function schedulePage() {
    window.clearTimeout(pageTimer);
    pageTimer = window.setTimeout(sendPage, 300);
  }

  // 切换身份时重新加载聊天页，丢弃旧身份的全部状态与在途请求。
  function reloadFrame() {
    frameReady = false;
    applyLayout();
    frame.src = frame.src;
  }

  function syncFrameState() {
    if (!frame.contentWindow) {
      return;
    }
    frame.contentWindow.postMessage(
      {
        type: "messenger:widget-state",
        visible: panel.dataset.open === "true",
        expanded: expanded,
        expandable: !isMobile(),
      },
      baseUrl,
    );
  }

  button.addEventListener("click", function () {
    setOpen(panel.dataset.open !== "true");
  });

  window.addEventListener("message", function (event) {
    if (
      preview &&
      event.source === window.parent &&
      event.data &&
      event.data.type === "messenger:preview-config"
    ) {
      previewParentOrigin = event.origin === "null" ? "" : event.origin;
      previewConfig = event.data;
      applyPreviewTheme(event.data.value || {});
      sendPreviewConfig();
      return;
    }
    if (event.origin !== baseUrl || event.source !== frame.contentWindow) {
      return;
    }
    if (!event.data || typeof event.data.type !== "string") {
      return;
    }
    if (event.data.type === "messenger:frame-ready") {
      frameReady = true;
      applyLayout();
      return;
    }
    if (event.data.type === "messenger:close") {
      setOpen(false);
      return;
    }
    if (event.data.type === "messenger:unread") {
      unreadBadge.hidden = event.data.unread !== true;
      return;
    }
    if (event.data.type === "messenger:identity-expired") {
      identityExpiredListeners.slice().forEach(function (listener) {
        try {
          listener();
        } catch (error) {
          console.warn(sdk.global + " identityExpired listener failed", error);
        }
      });
      return;
    }
    if (event.data.type === "messenger:preview-ready") {
      notifyPreviewReady();
      return;
    }
    if (event.data.type === "messenger:toggle-expand" && !isMobile()) {
      expanded = !expanded;
      applyLayout();
      syncFrameState();
    }
  });

  document.addEventListener("click", function (event) {
    var trigger = event.target.closest("[" + sdk.openAttribute + "]");
    if (!trigger) {
      return;
    }
    var targetId = trigger.getAttribute(sdk.openAttribute).trim();
    if (targetId !== "" && targetId.toLowerCase() !== channelId.toLowerCase()) {
      return;
    }
    event.preventDefault();
    setOpen(true);
  });

  mobileQuery.addEventListener("change", handleViewportModeChange);
  frame.addEventListener("load", function () {
    sendIdentity();
    sendPage();
    syncFrameState();
    sendPreviewConfig();
  });
  window.addEventListener("resize", applyLayout);
  if (window.visualViewport) {
    window.visualViewport.addEventListener("resize", applyLayout);
    window.visualViewport.addEventListener("scroll", applyLayout);
  }

  var api = {
    show: function () {
      setOpen(true);
    },
    hide: function () {
      setOpen(false);
    },
    setBottomInset: setBottomInset,
    // 以签名身份登录，已登录时直接替换当前用户。
    login: function (token) {
      customerToken = typeof token === "string" ? token : "";
      rotateVisitor = false;
      reloadFrame();
    },
    // 回到匿名访客并轮换匿名访客 Token。
    logout: function () {
      customerToken = "";
      rotateVisitor = true;
      reloadFrame();
    },
    // 订阅挂件事件，目前只有 identityExpired。
    on: function (name, listener) {
      if (name === "identityExpired" && typeof listener === "function") {
        identityExpiredListeners.push(listener);
      }
    },
  };
  window[sdk.global] = api;

  // 单页应用的路由变化后重新下发宿主页面。
  if (!preview) {
    ["pushState", "replaceState"].forEach(function (name) {
      var original = window.history[name];
      window.history[name] = function () {
        var result = original.apply(this, arguments);
        schedulePage();
        return result;
      };
    });
    window.addEventListener("popstate", schedulePage);
    window.addEventListener("hashchange", schedulePage);
  }

  shadow.appendChild(style);
  shadow.appendChild(panel);
  shadow.appendChild(button);
  document.documentElement.appendChild(root);
  if (preview) {
    setOpen(true);
  } else {
    applyLayout();
  }
})();
