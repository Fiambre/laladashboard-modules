//go:build wasip1

// Server Monitor module for LalaDashboard.
// Performs HTTP HEAD checks on configured URLs and displays latency with a color semaphore.
//
// Compile: GOOS=wasip1 GOARCH=wasm go build -o widget.wasm .
package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"unsafe"
)

func main() {}

// ---- WASM memory protocol -----------------------------------------------

var outBuf [1 << 20]byte // 1 MB output buffer
var outLen int32

func setOutput(s string) {
	n := copy(outBuf[:], s)
	outLen = int32(n)
}

//export get_output_ptr
func getOutputPtr() int32 { return int32(uintptr(unsafe.Pointer(&outBuf[0]))) }

//export get_output_len
func getOutputLen() int32 { return outLen }

var allocBuf []byte

//export alloc
func alloc(size uint32) uint32 {
	if uint32(cap(allocBuf)) < size {
		allocBuf = make([]byte, size)
	}
	allocBuf = allocBuf[:size]
	return uint32(uintptr(unsafe.Pointer(&allocBuf[0])))
}

// ---- host functions -------------------------------------------------------

// http_check makes a HEAD request and returns latency in ms (0 = down/timeout).
//go:wasmimport env http_check
func hostHTTPCheck(urlPtr, urlLen uint32) uint32

func httpCheck(rawURL string) uint32 {
	b := []byte(rawURL)
	if len(b) == 0 {
		return 0
	}
	return hostHTTPCheck(
		uint32(uintptr(unsafe.Pointer(&b[0]))),
		uint32(len(b)),
	)
}

// ---- module metadata ------------------------------------------------------

//export module_name
func moduleName() int32 {
	setOutput("Server Monitor")
	return 0
}

//export config_schema
func configSchema() int32 {
	setOutput(`[
  {"key":"servers","label":"Servidores (nombre|url, uno por línea)","type":"text","required":true,"default":"","placeholder":"Urek|https://urek.fiambre.dev\nKhun|https://khun.example.com"},
  {"key":"poll_seconds","label":"Intervalo (segundos)","type":"number","default":"30"}
]`)
	return 0
}

// ---- helpers ---------------------------------------------------------------

func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}

func dotClass(rtt uint32) string {
	if rtt == 0 {
		return "sm-red"
	} else if rtt < 200 {
		return "sm-green"
	}
	return "sm-yellow"
}

func barWidth(rtt uint32) string {
	if rtt == 0 {
		return "0"
	}
	pct := float64(rtt) / 1000.0 * 100.0
	if pct > 100 {
		pct = 100
	}
	return fmt.Sprintf("%.1f", pct)
}

func barColor(rtt uint32) string {
	if rtt < 200 {
		return "#4ade80"
	}
	return "#facc15"
}

// ---- render ---------------------------------------------------------------

//export render
func render(cfgPtr, cfgLen uint32) int32 {
	cfgBytes := make([]byte, cfgLen)
	for i := uint32(0); i < cfgLen; i++ {
		cfgBytes[i] = *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i)))
	}

	var settings map[string]string
	json.Unmarshal(cfgBytes, &settings)

	serversRaw := strings.TrimSpace(settings["servers"])
	if serversRaw == "" {
		setOutput(`<div class="sm-empty">Sin servidores configurados</div>` + smCSS)
		return 0
	}

	type srv struct{ name, url string }
	var servers []srv
	for _, line := range strings.Split(serversRaw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			servers = append(servers, srv{
				name: strings.TrimSpace(parts[0]),
				url:  strings.TrimSpace(parts[1]),
			})
		} else if len(parts) == 1 && parts[0] != "" {
			servers = append(servers, srv{name: parts[0], url: parts[0]})
		}
	}

	if len(servers) == 0 {
		setOutput(`<div class="sm-empty">Sin servidores configurados</div>` + smCSS)
		return 0
	}

	var sb strings.Builder
	sb.WriteString(`<div class="sm-widget">`)

	for _, s := range servers {
		rtt := httpCheck(s.url)
		cls := dotClass(rtt)

		sb.WriteString(`<div class="sm-row">`)
		sb.WriteString(fmt.Sprintf(`<span class="sm-dot %s"></span>`, cls))
		sb.WriteString(fmt.Sprintf(`<span class="sm-name">%s</span>`, esc(s.name)))

		if rtt > 0 {
			sb.WriteString(fmt.Sprintf(
				`<div class="sm-bar-wrap"><div class="sm-bar" style="width:%s%%;background:%s"></div></div>`,
				barWidth(rtt), barColor(rtt),
			))
			sb.WriteString(fmt.Sprintf(`<span class="sm-ms">%dms</span>`, rtt))
		} else {
			sb.WriteString(`<div class="sm-bar-wrap"></div>`)
			sb.WriteString(`<span class="sm-ms sm-down">DOWN</span>`)
		}

		sb.WriteString(`</div>`)
	}

	sb.WriteString(`</div>`)
	sb.WriteString(smCSS)
	setOutput(sb.String())
	return 0
}

const smCSS = `<style>
.sm-widget{display:flex;flex-direction:column;gap:.4rem}
.sm-empty{font-size:.78rem;color:rgba(255,255,255,.35)}
.sm-row{display:grid;grid-template-columns:8px 1fr 1fr 52px;align-items:center;gap:.55rem}
.sm-dot{width:8px;height:8px;border-radius:50%;flex-shrink:0}
.sm-green{background:#4ade80;box-shadow:0 0 5px #4ade8088}
.sm-yellow{background:#facc15;box-shadow:0 0 5px #facc1588}
.sm-red{background:#f87171;box-shadow:0 0 5px #f8717188;animation:sm-pulse 1.2s infinite}
@keyframes sm-pulse{0%,100%{opacity:1}50%{opacity:.25}}
.sm-name{font-size:.78rem;font-weight:300;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.sm-bar-wrap{height:3px;background:rgba(255,255,255,.1);border-radius:2px;overflow:hidden}
.sm-bar{height:100%;border-radius:2px;transition:width .4s ease}
.sm-ms{font-size:.7rem;text-align:right;font-variant-numeric:tabular-nums;color:rgba(255,255,255,.55)}
.sm-down{color:#f87171;font-weight:600}
</style>`
