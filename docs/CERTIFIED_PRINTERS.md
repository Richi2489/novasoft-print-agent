# Impresoras certificadas y soportadas

## Niveles de soporte

| Nivel | Significado |
|---|---|
| **Certified** | NovaSoft probó el modelo contra hardware real. Funciona end-to-end. |
| **Supported** | ESC/POS estándar, debe funcionar sin problemas. Sin validación formal. |
| **Experimental** | Reportada por la comunidad. Úsala bajo tu riesgo. |

## Certified

| Marca | Modelo | Interfaz | Papel | DPI | Validado en |
|---|---|---|---|---|---|
| **GHIA** | **GTP801** | USB + Ethernet | 80 mm | 203 | La Capital (Querétaro), 2 unidades en producción. USB primario, Ethernet backup. |

## Supported

| Marca | Modelo | Interfaz | Papel | Notas |
|---|---|---|---|---|
| **Genérico** | **POS-80 (ESC/POS)** | USB | 80 mm | Impresoras chinas genéricas comunes en Mercado Libre / Amazon. Cualquier modelo 80 mm USB que soporte ESC/POS estándar. El driver de Windows suele llamarse "Generic / Text Only" o "80mm Series Printer". |

## Experimental

_Ninguna reportada todavía._

---

## Heurística de auto-selección

Cuando hay varias impresoras instaladas en Windows, el agent busca la
primera cuyo nombre contenga alguna de estas palabras clave
(case-insensitive, substring match):

- `POS-80`, `POS80`, `POS 80`, `THERMAL`
- `GHIA`, `GTP801`
- `EPSON`, `TM-`, `TM `
- `STAR`, `TSP`
- `BIXOLON`, `SRP-`

Si ninguna coincide, el agent usa **la primera impresora no-virtual** de
la lista como fallback, y loggea cuál eligió — el admin puede verificar
con `novasoft-agent.exe status`.

## Impresoras virtuales filtradas

El agent **ignora** estas impresoras (no son hardware real):

- Microsoft Print to PDF
- Microsoft XPS Document Writer
- Fax
- OneNote
- Send to OneNote

## ¿Tu impresora no aparece en la lista?

Si tienes una impresora 80 mm compatible con ESC/POS estándar, es muy
probable que funcione aunque no esté documentada. Pasos para certificarla:

1. Instálala en Windows con su driver oficial.
2. Verifica que imprima una página de prueba desde Windows.
3. Ejecuta `novasoft-agent.exe pair` + `run` como describe `SETUP.md`.
4. Genera una cuenta previa desde el POS.
5. Si el ticket sale legible, corta bien, y los totales se alinean:
   - Abre un issue en
     https://github.com/Richi2489/novasoft-print-agent/issues con:
     - Marca / modelo exacto.
     - Nombre del driver en Windows.
     - Foto de un ticket real (borra datos sensibles).

La agregaremos a la tabla **Supported**.

## Si tu impresora NO funciona

ESC/POS es un pseudo-estándar — algunos modelos chinos económicos
implementan subsets distintos. Síntomas típicos de incompatibilidad:

| Síntoma | Causa probable |
|---|---|
| Ticket sale en blanco | El driver de Windows está transformando los bytes (GDI). Busca modo "Raw Print" o cambia a driver genérico ESC/POS. |
| Caracteres raros | Codepage distinta. Fase 1a asume CP437/CP850 (el default de la mayoría). |
| No corta | Tu impresora no soporta `GS V 1` (corte parcial). Reporta el modelo. |
| Texto pequeño / grande incorrectamente | Tu impresora usa `GS !` de forma no estándar. |

En cualquiera de estos casos: abre un issue y adjunta foto + modelo.
Intentaremos dar workaround o agregar un dispatch específico del modelo.
