#!/bin/sh

if command -v update-desktop-database >/dev/null 2>&1; then
  echo "Updating desktop application database"
  update-desktop-database -q /usr/share/applications
else
  echo "Warning: update-desktop-database is unavailable; application menus may update later" >&2
fi

exit 0
