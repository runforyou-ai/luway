/** 美式英语·连接文案。 */
const connection = {
  title: "Connect to a server",
  description: "Enter the server address.",
  serverUrlLabel: "Server address",
  serverUrlRequired: "Enter the server address.",
  serverUrlInvalid: "Enter a complete and valid server address.",
  detect: "Detect",
  detecting: "Detecting…",
  connect: "Connect",
  connecting: "Connecting…",
  connectionError:
    "Could not connect to this server. Check the address and try again.",
  savedServerUnreachable: "Can't reach {{host}} right now. Check your network and try again.",
  serverNotInstalled: "{{host}} hasn't been set up yet. Open this address in a browser to finish setup first.",
  serverOutdated: "The server at {{host}} is out of date. Ask your administrator to update it.",
  upgrade: {
    title: "Update required",
    description: "{{host}} requires a newer version of this app.",
    download: "Download new version",
    downloading: "Downloading new version",
    changeServer: "Change server",
  },
  update: {
    ready: "Version {{version}} is ready. Restart to apply it.",
    restart: "Restart to update",
    available: "Version {{version}} is available. Install it from the server's download page.",
    download: "Go to downloads",
  },
  clientLink: {
    title: "Use in the app",
    description: "The app opens with this server filled in. Confirm to connect.",
    desktop: "Desktop",
    desktopHelp: "Open the desktop app if it is installed, or download it first.",
    openDesktop: "Open desktop app",
    downloadDesktop: "Download desktop app",
    mobile: "Mobile",
    mobileHelp: "If the mobile app is installed, scan with your phone camera to open it.",
    qrCodeAlt: "Mobile app connection QR code",
  },
}

export default connection
