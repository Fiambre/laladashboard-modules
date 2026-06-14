//go:build wasip1

// twitter-trends: muestra los trending topics de Twitter/X para un país via trends24.in
// Compile: GOOS=wasip1 GOARCH=wasm go build -o widget.wasm .
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unsafe"
)

// ---- host function -----------------------------------------------------------

//go:wasmimport env http_get
func hostHTTPGet(urlPtr, urlLen, resultPtr uint32) uint32

var httpBuf [1 << 20]byte // 1 MB

func httpGet(rawURL string) (string, bool) {
	urlB := []byte(rawURL)
	if len(urlB) == 0 {
		return "", false
	}
	n := hostHTTPGet(
		uint32(uintptr(unsafe.Pointer(&urlB[0]))), uint32(len(urlB)),
		uint32(uintptr(unsafe.Pointer(&httpBuf[0]))),
	)
	if n == 0 {
		return "", false
	}
	return string(httpBuf[:n]), true
}

// ---- HTML helpers ------------------------------------------------------------

func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}

// extractTrends scans for occurrences of class=trend-link>TEXT< in the HTML.
func extractTrends(html string, max int) []string {
	const marker = `class=trend-link>`
	var trends []string
	s := html
	for len(trends) < max {
		i := strings.Index(s, marker)
		if i < 0 {
			break
		}
		s = s[i+len(marker):]
		j := strings.IndexByte(s, '<')
		if j < 0 {
			break
		}
		trend := strings.TrimSpace(s[:j])
		if trend != "" {
			trends = append(trends, trend)
		}
		s = s[j:]
	}
	return trends
}

// ---- main -------------------------------------------------------------------

func main() {
	var settings map[string]string
	json.NewDecoder(os.Stdin).Decode(&settings)

	country := strings.TrimSpace(settings["country"])
	if country == "" {
		country = "chile"
	}
	max := 15
	if v, _ := strconv.Atoi(strings.TrimSpace(settings["max_items"])); v > 0 {
		max = v
	}

	rawURL := "https://trends24.in/" + country + "/"
	html, ok := httpGet(rawURL)
	if !ok {
		fmt.Print(`<div class="tt-error">No se pudo cargar trends24.in</div>` + ttCSS)
		return
	}

	trends := extractTrends(html, max)
	if len(trends) == 0 {
		fmt.Print(`<div class="tt-error">Sin tendencias (¿país válido?)</div>` + ttCSS)
		return
	}

	var sb strings.Builder
	sb.WriteString(`<div class="tt-widget"><ol class="tt-list">`)
	for _, t := range trends {
		sb.WriteString(`<li class="tt-item">`)
		sb.WriteString(esc(t))
		sb.WriteString(`</li>`)
	}
	sb.WriteString(`</ol></div>`)
	sb.WriteString(ttCSS)
	fmt.Print(sb.String())
}

const ttCSS = `<style>
.tt-widget{padding:.4rem .6rem;height:100%;overflow-y:auto;box-sizing:border-box}
.tt-list{list-style:none;margin:0;padding:0;display:flex;flex-direction:column;gap:.18rem;counter-reset:tt}
.tt-item{display:flex;align-items:baseline;gap:.45rem;font-size:.8rem;line-height:1.3;padding:.12rem 0;border-bottom:1px solid rgba(255,255,255,.06)}
.tt-item:last-child{border-bottom:none}
.tt-item::before{counter-increment:tt;content:counter(tt);font-size:.68rem;color:rgba(255,255,255,.28);min-width:16px;text-align:right;flex-shrink:0}
.tt-error{font-size:.78rem;color:#f87171;padding:.4rem 0}
</style>`
