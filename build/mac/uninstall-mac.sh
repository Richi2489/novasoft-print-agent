#!/bin/bash
# NovaSoft Print Agent — desinstalador para macOS.
#
#   bash uninstall-mac.sh            # quita el agente, conserva el emparejamiento
#   bash uninstall-mac.sh --todo     # además borra config (token) y logs
#
# No borra la cola de impresora del Mac (puede usarla otro programa).

set -u
DEST="$HOME/Library/Application Support/NovaSoftPrintAgent"
EXE="$DEST/novasoft-agent"
PLIST="$HOME/Library/LaunchAgents/mx.novasoft.printagent.plist"

if [ -x "$EXE" ]; then
  "$EXE" service uninstall || true
else
  launchctl bootout "gui/$(id -u)/mx.novasoft.printagent" 2>/dev/null \
    || launchctl unload -w "$PLIST" 2>/dev/null || true
  rm -f "$PLIST"
fi
rm -f "$EXE"
echo "✓ Agente quitado."

if [ "${1:-}" = "--todo" ]; then
  rm -rf "$DEST"
  echo "✓ Config y logs borrados ($DEST)."
  echo "  En NovaSoft, borra también la impresora vieja (Configuración → Impresoras)."
else
  echo "  El emparejamiento y los logs siguen en: $DEST"
  echo "  (para borrarlos: bash uninstall-mac.sh --todo)"
fi
