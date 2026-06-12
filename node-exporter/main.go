//go:build wasip1

// Node Exporter module for LalaDashboard.
// Fetches Prometheus metrics from one or more node_exporter instances and
// renders CPU load, RAM, and uptime — or "OFFLINE" if unreachable.
//
// Compile: GOOS=wasip1 GOARCH=wasm go build -o widget.wasm .
package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

func main() {}

// ---- WASM memory protocol -----------------------------------------------

var outBuf [2 << 20]byte // 2 MB output buffer
var outLen int32

func setOutput(s string) {
	n := copy(outBuf[:], s)
	outLen = int32(n)
}

//go:wasmexport get_output_ptr
func getOutputPtr() int32 { return int32(uintptr(unsafe.Pointer(&outBuf[0]))) }

//go:wasmexport get_output_len
func getOutputLen() int32 { return outLen }

//go:wasmexport alloc
func alloc(size uint32) uint32 {
	b := make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&b[0])))
}

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

// ---- module metadata ------------------------------------------------------

//go:wasmexport module_name
func moduleName() int32 {
	setOutput("Node Exporter")
	return 0
}

//go:wasmexport config_schema
func configSchema() int32 {
	setOutput(`[
  {"key":"servers","label":"Servidores (nombre|url separados por coma)","type":"text","required":true,
   "default":"mi-servidor|http://localhost:9100",
   "placeholder":"web1|http://192.168.1.10:9100,db1|http://192.168.1.11:9100"},
  {"key":"poll_seconds","label":"Intervalo (segundos)","type":"number","default":"30"}
]`)
	return 0
}

// ---- Prometheus text format parser ----------------------------------------

// parseScalar finds the value of a bare (label-free) metric in Prometheus text format.
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

func loadColor(load1 float64) string {
	if load1 >= 4 {
		return "#f87171"
	} else if load1 >= 2 {
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

// ---- render ---------------------------------------------------------------

//go:wasmexport render
func render(cfgPtr, cfgLen uint32) int32 {
	cfgBytes := make([]byte, cfgLen)
	for i := uint32(0); i < cfgLen; i++ {
		cfgBytes[i] = *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i)))
	}

	var settings map[string]string
	json.Unmarshal(cfgBytes, &settings)

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
		setOutput(`<div class="ne-empty">Sin servidores configurados</div>` + neCSS)
		return 0
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
		load5, _ := parseScalar(body, "node_load5")
		load15, _ := parseScalar(body, "node_load15")
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
			sb.WriteString(fmt.Sprintf(
				`<div class="ne-row">`+
					`<span class="ne-lbl">Load</span>`+
					`<span class="ne-val" style="color:%s">%.2f</span>`+
					`<span class="ne-sub">%.2f&nbsp;·&nbsp;%.2f</span>`+
					`</div>`,
				loadColor(load1), load1, load5, load15,
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

		sb.WriteString(`</div></div>`) // ne-stats, ne-srv
	}

	sb.WriteString(`</div>`)
	sb.WriteString(neCSS)
	setOutput(sb.String())
	return 0
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
