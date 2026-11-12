#!/bin/bash
# NovaSoft Print Agent — instalador para macOS.
#
# Uso (en Terminal, dentro de la carpeta descomprimida):
#   bash install-mac.sh                    # pregunta el código
#   bash install-mac.sh ABCD-1234-WXYZ     # con código
#
# Qué hace:
#   1. Elige el binario según el chip y la versión de macOS.
#   2. Lo copia a ~/Library/Application Support/NovaSoftPrintAgent/ y le
#      quita la cuarentena de Gatekeeper.
#   3. Empareja con el código de NovaSoft (si no estaba emparejado).
#   4. Elige la impresora: cola CUPS (USB) o IP:9100 (red). Si hay una
#      térmica USB sin cola, ofrece crearla en modo "raw".
#   5. Imprime una prueba.
#   6. Instala el LaunchAgent (arranca ya, al iniciar sesión y se
#      reinicia solo si se cae).
# No pide sudo salvo, si hace falta, para crear la cola de la impresora.

set -u

DIR="$(cd "$(dirname "$0")" && pwd)"
DEST="$HOME/Library/Application Support/NovaSoftPrintAgent"
EXE="$DEST/novasoft-agent"
CODE="${1:-}"

say()  { printf '\n\033[1m%s\033[0m\n' "$*"; }
ok()   { printf '  \033[32m✓\033[0m %s\n' "$*"; }
warn() { printf '  \033[33m!\033[0m %s\n' "$*"; }
die()  { printf '\n\033[31m✗ %s\033[0m\n' "$*"; exit 1; }

say "=== NovaSoft Print Agent — instalación en Mac ==="

# ---------------------------------------------------------------- 1. binario
OSVER="$(sw_vers -productVersion 2>/dev/null || echo 0)"
MAJOR="$(echo "$OSVER" | cut -d. -f1)"
MINOR="$(echo "$OSVER" | cut -d. -f2)"
[ -z "$MINOR" ] && MINOR=0
# Apple Silicon aunque la Terminal corra bajo Rosetta.
if [ "$(sysctl -n hw.optional.arm64 2>/dev/null)" = "1" ]; then CHIP="arm64"; else CHIP="intel"; fi

if [ "$MAJOR" -eq 10 ] && [ "$MINOR" -lt 13 ]; then
  die "macOS $OSVER es demasiado viejo. Se necesita macOS 10.13 High Sierra o más nuevo."
fi
# mac-intel: Go 1.20, macOS 10.13+ (Mojave incluido).
# mac-arm64: Go del repo, macOS 12+. Un Apple Silicon con macOS 11 usa el
# de Intel vía Rosetta 2.
if [ "$CHIP" = "arm64" ] && [ "$MAJOR" -ge 12 ] && [ -f "$DIR/bin/novasoft-agent-mac-arm64" ]; then
  BIN="$DIR/bin/novasoft-agent-mac-arm64"
else
  BIN="$DIR/bin/novasoft-agent-mac-intel"
fi
# También vale el binario suelto junto al script.
[ -f "$BIN" ] || BIN="$DIR/$(basename "$BIN")"
[ -f "$BIN" ] || die "No encuentro $(basename "$BIN"). ¿Descomprimiste la carpeta completa?"
ok "macOS $OSVER, chip $CHIP → $(basename "$BIN")"

# ---------------------------------------------------------------- 2. copiar
mkdir -p "$DEST" || die "No pude crear $DEST"
# Si ya corría una versión anterior, detenerla antes de reemplazar.
if [ -x "$EXE" ]; then "$EXE" service stop >/dev/null 2>&1 || true; fi
cp -f "$BIN" "$EXE" || die "No pude copiar el agente a $DEST"
chmod +x "$EXE"
xattr -dr com.apple.quarantine "$EXE" 2>/dev/null || true
xattr -dr com.apple.quarantine "$DIR" 2>/dev/null || true
"$EXE" version >/dev/null 2>&1 || die "El agente no arranca en este Mac. Si macOS dice que \"no se puede abrir\", ve a Preferencias del Sistema → Seguridad y privacidad → \"Abrir de todos modos\" y vuelve a correr este script."
ok "Agente $("$EXE" version) en $EXE"

# ---------------------------------------------------------------- 3. emparejar
if [ -f "$DEST/config.json" ] && [ -z "$CODE" ]; then
  ok "Ya estaba emparejado (se conserva; NO hace falta código nuevo)."
else
  for intento in 1 2 3; do
    if [ -z "$CODE" ]; then
      printf '\n  Escribe el código que aparece en NovaSoft (ej. ABCD-1234-WXYZ): '
      read -r CODE
    fi
    "$EXE" pair "$CODE"
    rc=$?
    if [ "$rc" -eq 0 ]; then PAIRED=1; break; fi
    case $rc in
      2) warn "Código mal escrito." ;;
      3) warn "Ese código ya se usó. Genera otro en NovaSoft." ;;
      4) warn "El código venció (duran 10 min). Genera otro en NovaSoft." ;;
      5) warn "Sin internet o sin acceso a api.novasoft.mx." ;;
      *) warn "No se pudo emparejar (código $rc)." ;;
    esac
    CODE=""
  done
  [ "${PAIRED:-0}" = "1" ] || die "No se emparejó. Vuelve a correr: bash install-mac.sh"
fi

# ---------------------------------------------------------------- 4. impresora
colas() {
  # lpstat -e: un nombre de cola por línea. Respaldo: lpstat -p.
  if ! LC_ALL=C lpstat -e 2>/dev/null | grep -v ' '; then
    LC_ALL=C lpstat -p 2>/dev/null | awk '$1 == "printer" {print $2}'
  fi
}

crear_cola_usb() {
  say "Buscando impresoras USB conectadas…"
  USBS=()
  # lpinfo puede pedir permisos de administrador (CUPS-Get-Devices).
  DEVS="$(LC_ALL=C lpinfo -v 2>/dev/null)"
  if ! echo "$DEVS" | grep -q 'usb:'; then
    echo "  (puede pedir tu contraseña de Mac)"
    DEVS="$(LC_ALL=C sudo lpinfo -v 2>/dev/null)"
  fi
  while IFS= read -r line; do [ -n "$line" ] && USBS+=("$line"); done < <(echo "$DEVS" | awk '$2 ~ /^usb:/ {print $2}')
  if [ "${#USBS[@]}" -eq 0 ]; then
    warn "No veo ninguna impresora USB. Revisa el cable y que esté encendida."
    return 1
  fi
  i=1; for u in "${USBS[@]}"; do echo "    $i. $u"; i=$((i+1)); done
  printf '  Número de la impresora térmica (Enter = 1): '
  read -r n; [ -z "$n" ] && n=1
  URI="${USBS[$((n-1))]:-}"
  [ -n "$URI" ] || { warn "Número inválido."; return 1; }
  COLA="NovaSoft_POS"
  PPD_GEN="/System/Library/Frameworks/ApplicationServices.framework/Versions/A/Frameworks/PrintCore.framework/Resources/Generic.ppd"
  echo "  Creando la cola $COLA (puede pedir tu contraseña de Mac)…"
  if lpadmin -p "$COLA" -E -v "$URI" -m raw 2>/dev/null \
     || sudo lpadmin -p "$COLA" -E -v "$URI" -m raw 2>/dev/null \
     || sudo lpadmin -p "$COLA" -E -v "$URI" -P "$PPD_GEN"; then
    cupsenable "$COLA" 2>/dev/null || sudo cupsenable "$COLA" 2>/dev/null || true
    cupsaccept "$COLA" 2>/dev/null || sudo cupsaccept "$COLA" 2>/dev/null || true
    ok "Cola $COLA creada."
    return 0
  fi
  warn "No pude crear la cola. Agrégala a mano (ver INSTALAR-MAC.md, paso 2)."
  return 1
}

say "Impresora"
LISTA=()
while IFS= read -r c; do [ -n "$c" ] && LISTA+=("$c"); done < <(colas)
if [ "${#LISTA[@]}" -eq 0 ]; then
  warn "El Mac no tiene impresoras agregadas."
  printf '  ¿Es USB y quieres que la agregue yo? (S/n): '
  read -r r
  if [ "$r" != "n" ] && [ "$r" != "N" ] && crear_cola_usb; then
    LISTA=(); while IFS= read -r c; do [ -n "$c" ] && LISTA+=("$c"); done < <(colas)
  fi
fi

echo "  Impresoras del Mac:"
if [ "${#LISTA[@]}" -eq 0 ]; then
  echo "    (ninguna)"
else
  i=1; for c in "${LISTA[@]}"; do echo "    $i. $c"; i=$((i+1)); done
fi
echo "  Escribe el NÚMERO de la térmica,"
echo "  o su IP si es de red (ej. 192.168.1.50),"
echo "  o U para agregar una térmica USB nueva."
printf '  > '
read -r SEL
if [ "$SEL" = "U" ] || [ "$SEL" = "u" ]; then
  if crear_cola_usb; then SEL="NovaSoft_POS"; else SEL=""; fi
elif echo "$SEL" | grep -Eq '^[0-9]+$' && [ "$SEL" -ge 1 ] && [ "$SEL" -le "${#LISTA[@]}" ]; then
  SEL="${LISTA[$((SEL-1))]}"
fi
if [ -n "$SEL" ]; then
  "$EXE" printer set "$SEL" || warn "No pude fijar la impresora."
else
  warn "Sin impresora fija: el agente elegirá sola (puede equivocarse si hay varias)."
fi

# ---------------------------------------------------------------- 5. prueba
printf '\n  ¿Imprimir un ticket de prueba ahora? (S/n): '
read -r r
if [ "$r" != "n" ] && [ "$r" != "N" ]; then
  "$EXE" test-print || warn "La prueba falló — revisa INSTALAR-MAC.md, sección Problemas."
fi

# ---------------------------------------------------------------- 6. launchd
say "Arranque automático"
"$EXE" service install || die "No pude instalar el arranque automático."

say "Listo ✓"
echo "  El agente ya está corriendo y arrancará solo al iniciar sesión."
echo "  En NovaSoft (Configuración → Impresoras) debe aparecer EN LÍNEA."
echo "  Estado:      \"$EXE\" status"
echo "  Log:         $DEST/logs/agent.log"
echo "  Desinstalar: bash uninstall-mac.sh"
