//go:build wasip1

// Node Exporter module for LalaDashboard.
// Reads config JSON from stdin, writes rendered HTML to stdout.
//
// Compile: GOOS=wasip1 GOARCH=wasm go build -o widget.wasm .
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

// ---- host functions -------------------------------------------------------

//go:wasmimport env http_get
func hostHTTPGet(urlPtr, urlLen, resultPtr uint32) uint32

var httpBuf [1 << 20]byte // 1 MB for metrics response

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

// ---- Prometheus text format parser ----------------------------------------

func parseScalar(body, name string) (float64, bool) {
	needle := name + " "
	for _, line := range strings.Split(body, "\n") {
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		if strings.HasPrefix(line, needle) {
			parts := strings.Fields(line[len(needle):])
			if len(parts) > 0 {
				if v, err := strconv.ParseFloat(parts[0], 64); err == nil {
					return v, true
				}
			}
		}
	}
	return 0, false
}

// countCPUs counts logical CPUs by counting node_cpu_seconds_total{mode="idle"} entries.
func countCPUs(body string) int {
	n := 0
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "node_cpu_seconds_total{") && strings.Contains(line, `mode="idle"`) {
			n++
		}
	}
	if n == 0 {
		return 1
	}
	return n
}

// ---- helpers ---------------------------------------------------------------

func fmtBytes(bytes float64) string {
	switch {
	case bytes >= 1e12:
		return fmt.Sprintf("%.1fTB", bytes/1e12)
	case bytes >= 1e9:
		return fmt.Sprintf("%.1fGB", bytes/1e9)
	case bytes >= 1e6:
		return fmt.Sprintf("%.0fMB", bytes/1e6)
	default:
		return fmt.Sprintf("%.0fB", bytes)
	}
}

func fmtUptime(sec float64) string {
	d := int(sec) / 86400
	h := (int(sec) % 86400) / 3600
	m := (int(sec) % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%dd %dh", d, h)
	}
	if h > 0 {
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func barColor(pct float64) string {
	if pct >= 90 {
		return "#f87171"
	} else if pct >= 70 {
		return "#facc15"
	}
	return "#4ade80"
}


func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}

// ---- main -----------------------------------------------------------------

func main() {
	var settings map[string]string
	json.NewDecoder(os.Stdin).Decode(&settings)

	serversRaw := settings["servers"]
	if serversRaw == "" {
		serversRaw = "localhost|http://localhost:9100"
	}

	type srv struct{ name, url string }
	var servers []srv
	for _, entry := range strings.Split(serversRaw, ",") {
		entry = strings.TrimSpace(entry)
		parts := strings.SplitN(entry, "|", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			servers = append(servers, srv{
				name: strings.TrimSpace(parts[0]),
				url:  strings.TrimRight(strings.TrimSpace(parts[1]), "/"),
			})
		}
	}
	if len(servers) == 0 {
		fmt.Print(`<div class="ne-empty">Sin servidores configurados</div>` + neCSS)
		return
	}

	now := float64(time.Now().Unix())
	var sb strings.Builder
	sb.WriteString(`<div class="ne-wrap">`)

	for _, s := range servers {
		body, ok := httpGet(s.url + "/metrics")

		if !ok || !strings.Contains(body, "node_") {
			sb.WriteString(`<div class="ne-srv ne-offline">`)
			sb.WriteString(`<div class="ne-head">`)
			sb.WriteString(`<span class="ne-name">` + esc(s.name) + `</span>`)
			sb.WriteString(`<span class="ne-dot ne-dot-off"></span>`)
			sb.WriteString(`<span class="ne-offline-label">OFFLINE</span>`)
			sb.WriteString(`</div></div>`)
			continue
		}

		load1, hasLoad := parseScalar(body, "node_load1")
		memTotal, hasRAM := parseScalar(body, "node_memory_MemTotal_bytes")
		memAvail, _ := parseScalar(body, "node_memory_MemAvailable_bytes")
		bootTime, hasUptime := parseScalar(body, "node_boot_time_seconds")

		sb.WriteString(`<div class="ne-srv ne-online">`)
		sb.WriteString(`<div class="ne-head">`)
		sb.WriteString(`<span class="ne-name">` + esc(s.name) + `</span>`)
		sb.WriteString(`<span class="ne-dot ne-dot-on"></span>`)
		sb.WriteString(`</div>`)
		sb.WriteString(`<div class="ne-stats">`)

		if hasLoad {
			cpus := countCPUs(body)
			cpuPct := load1 / float64(cpus) * 100
			if cpuPct > 100 {
				cpuPct = 100
			}
			sb.WriteString(fmt.Sprintf(
				`<div class="ne-row">`+
					`<span class="ne-lbl">CPU</span>`+
					`<span class="ne-val" style="color:%s">%.0f%%</span>`+
					`</div>`+
					`<div class="ne-bar"><div class="ne-fill" style="width:%.0f%%;background:%s"></div></div>`,
				barColor(cpuPct), cpuPct, cpuPct, barColor(cpuPct),
			))
		}

		if hasRAM && memTotal > 0 {
			memUsed := memTotal - memAvail
			pct := 100 * memUsed / memTotal
			sb.WriteString(fmt.Sprintf(
				`<div class="ne-row">`+
					`<span class="ne-lbl">RAM</span>`+
					`<span class="ne-val">%s</span>`+
					`<span class="ne-sub">/&nbsp;%s</span>`+
					`</div>`+
					`<div class="ne-bar"><div class="ne-fill" style="width:%.0f%%;background:%s"></div></div>`,
				fmtBytes(memUsed), fmtBytes(memTotal), pct, barColor(pct),
			))
		}

		if hasUptime && bootTime > 0 {
			if up := now - bootTime; up > 0 {
				sb.WriteString(fmt.Sprintf(
					`<div class="ne-row">`+
						`<span class="ne-lbl">Up</span>`+
						`<span class="ne-val">%s</span>`+
						`</div>`,
					fmtUptime(up),
				))
			}
		}

		sb.WriteString(`</div></div>`)
	}

	sb.WriteString(`</div>`)
	sb.WriteString(neCSS)
	fmt.Print(sb.String())
}

const neCSS = `<style>
.ne-wrap{display:flex;flex-direction:column;gap:.85rem}
.ne-srv{padding:.4rem 0;border-bottom:1px solid rgba(255,255,255,.07)}
.ne-srv:last-child{border-bottom:none}
.ne-offline .ne-name{opacity:.4}
.ne-head{display:flex;align-items:center;gap:.55rem;margin-bottom:.4rem}
.ne-name{font-size:.85rem;font-weight:300;letter-spacing:.09em;text-transform:uppercase}
.ne-dot{width:6px;height:6px;border-radius:50%;flex-shrink:0}
.ne-dot-on{background:#4ade80;box-shadow:0 0 5px #4ade8088}
.ne-dot-off{background:#f87171}
.ne-offline-label{font-size:.62rem;letter-spacing:.14em;color:#f87171;text-transform:uppercase}
.ne-stats{display:flex;flex-direction:column;gap:.22rem}
.ne-row{display:flex;align-items:baseline;gap:.45rem}
.ne-lbl{font-size:.62rem;color:rgba(255,255,255,.32);width:2.6rem;flex-shrink:0;letter-spacing:.07em;text-transform:uppercase}
.ne-val{font-size:.95rem;font-weight:200}
.ne-sub{font-size:.7rem;color:rgba(255,255,255,.32)}
.ne-bar{width:100%;height:2px;background:rgba(255,255,255,.1);border-radius:1px;margin:.1rem 0 .25rem}
.ne-fill{height:100%;border-radius:1px}
.ne-empty{font-size:.8rem;color:rgba(255,255,255,.35)}
</style>`
