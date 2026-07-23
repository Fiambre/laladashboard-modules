//go:build wasip1

// Claude Rate Limit module for LalaDashboard.
// Reads config JSON from stdin, writes rendered HTML to stdout.
//
// Compile: GOOS=wasip1 GOARCH=wasm go build -o widget.wasm .
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unsafe"
)

// ---- host functions -------------------------------------------------------

//go:wasmimport env http_get
func hostHTTPGet(urlPtr, urlLen, resultPtr uint32) uint32

var httpBuf [1 << 20]byte

func httpGet(url string) (string, bool) {
	b := []byte(url)
	if len(b) == 0 {
		return "", false
	}
	n := hostHTTPGet(
		uint32(uintptr(unsafe.Pointer(&b[0]))),
		uint32(len(b)),
		uint32(uintptr(unsafe.Pointer(&httpBuf[0]))),
	)
	if n == 0 {
		return "", false
	}
	return string(httpBuf[:n]), true
}

// ---- response shape ---------------------------------------------------------

type usageResp struct {
	FiveHourUsedPct  int    `json:"five_hour_used_pct"`
	FiveHourResetsAt int64  `json:"five_hour_resets_at"`
	SevenDayUsedPct  int    `json:"seven_day_used_pct"`
	SevenDayResetsAt int64  `json:"seven_day_resets_at"`
	UpdatedAt        string `json:"updatedAt"`
}

// ---- helpers ---------------------------------------------------------------

func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}

func barColor(pct int) string {
	if pct >= 90 {
		return "#f87171"
	} else if pct >= 70 {
		return "#facc15"
	}
	return "#4ade80"
}

func clampPct(pct int) int {
	if pct < 0 {
		return 0
	}
	if pct > 100 {
		return 100
	}
	return pct
}

// fmtCountdown renders the time remaining until a unix timestamp.
func fmtCountdown(resetAt int64, now int64) string {
	sec := resetAt - now
	if resetAt <= 0 || sec <= 0 {
		return "reiniciando…"
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("resetea en %dd %dh", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("resetea en %dh %dm", h, m)
	}
	return fmt.Sprintf("resetea en %dm", m)
}

func row(label string, pct int, resetAt, now int64) string {
	pct = clampPct(pct)
	color := barColor(pct)
	return fmt.Sprintf(
		`<div class="cr-row">`+
			`<div class="cr-head"><span class="cr-lbl">%s</span><span class="cr-val" style="color:%s">%d%%</span></div>`+
			`<div class="cr-bar"><div class="cr-fill" style="width:%d%%;background:%s"></div></div>`+
			`<div class="cr-reset">%s</div>`+
			`</div>`,
		esc(label), color, pct, pct, color, esc(fmtCountdown(resetAt, now)),
	)
}

// ---- main -----------------------------------------------------------------

func main() {
	var settings map[string]string
	json.NewDecoder(os.Stdin).Decode(&settings)

	webhookURL := strings.TrimSpace(settings["webhook_url"])
	if webhookURL == "" {
		fmt.Print(`<div class="cr-empty">Sin webhook configurado</div>` + crCSS)
		return
	}

	body, ok := httpGet(webhookURL)
	if !ok || body == "" {
		fmt.Print(`<div class="cr-empty cr-error">No se pudo contactar el webhook</div>` + crCSS)
		return
	}

	var data usageResp
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		fmt.Print(`<div class="cr-empty cr-error">Respuesta inválida del webhook</div>` + crCSS)
		return
	}

	now := time.Now().Unix()

	var sb strings.Builder
	sb.WriteString(`<div class="cr-wrap">`)
	sb.WriteString(row("5 HORAS", data.FiveHourUsedPct, data.FiveHourResetsAt, now))
	sb.WriteString(row("7 DÍAS", data.SevenDayUsedPct, data.SevenDayResetsAt, now))
	sb.WriteString(`</div>`)
	sb.WriteString(crCSS)
	fmt.Print(sb.String())
}

const crCSS = `<style>
.cr-wrap{display:flex;flex-direction:column;gap:.75rem}
.cr-row{display:flex;flex-direction:column;gap:.25rem}
.cr-head{display:flex;justify-content:space-between;align-items:baseline}
.cr-lbl{font-size:.68rem;letter-spacing:.09em;color:rgba(255,255,255,.45);text-transform:uppercase}
.cr-val{font-size:1rem;font-weight:300;font-variant-numeric:tabular-nums}
.cr-bar{width:100%;height:4px;background:rgba(255,255,255,.1);border-radius:2px;overflow:hidden}
.cr-fill{height:100%;border-radius:2px;transition:width .4s ease}
.cr-reset{font-size:.65rem;color:rgba(255,255,255,.32);text-align:right}
.cr-empty{font-size:.8rem;color:rgba(255,255,255,.35)}
.cr-error{color:#f87171}
</style>`
