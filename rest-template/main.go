//go:build wasip1

// REST Template module for LalaDashboard.
// Fetches JSON from a configurable REST endpoint and renders it through a
// user-supplied HTML template. Reads config JSON from stdin, writes
// rendered HTML to stdout.
//
// Compile: GOOS=wasip1 GOARCH=wasm go build -o widget.wasm .
package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"math"
	"os"
	"strconv"
	"strings"
	"unsafe"
)

// ---- host functions -------------------------------------------------------

//go:wasmimport env http_get
func hostHTTPGet(urlPtr, urlLen, resultPtr uint32) uint32

//go:wasmimport env http_post
func hostHTTPPost(urlPtr, urlLen, bodyPtr, bodyLen, resultPtr uint32) uint32

//go:wasmimport env http_post_auth
func hostHTTPPostAuth(urlPtr, urlLen, bodyPtr, bodyLen, authPtr, authLen, resultPtr uint32) uint32

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

func httpPost(url, body string) (string, bool) {
	urlBytes := []byte(url)
	bodyBytes := []byte(body)
	if len(urlBytes) == 0 {
		return "", false
	}
	if len(bodyBytes) == 0 {
		bodyBytes = []byte("{}")
	}
	n := hostHTTPPost(
		uint32(uintptr(unsafe.Pointer(&urlBytes[0]))),
		uint32(len(urlBytes)),
		uint32(uintptr(unsafe.Pointer(&bodyBytes[0]))),
		uint32(len(bodyBytes)),
		uint32(uintptr(unsafe.Pointer(&httpBuf[0]))),
	)
	if n == 0 {
		return "", false
	}
	return string(httpBuf[:n]), true
}

func httpPostAuth(url, body, token string) (string, bool) {
	urlBytes := []byte(url)
	bodyBytes := []byte(body)
	authBytes := []byte(token)
	if len(urlBytes) == 0 {
		return "", false
	}
	if len(bodyBytes) == 0 {
		bodyBytes = []byte("{}")
	}
	n := hostHTTPPostAuth(
		uint32(uintptr(unsafe.Pointer(&urlBytes[0]))),
		uint32(len(urlBytes)),
		uint32(uintptr(unsafe.Pointer(&bodyBytes[0]))),
		uint32(len(bodyBytes)),
		uint32(uintptr(unsafe.Pointer(&authBytes[0]))),
		uint32(len(authBytes)),
		uint32(uintptr(unsafe.Pointer(&httpBuf[0]))),
	)
	if n == 0 {
		return "", false
	}
	return string(httpBuf[:n]), true
}

// ---- helpers ---------------------------------------------------------------

func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}

// normalizeJSON walks the decoded JSON value and turns float64 leaves into
// plain-formatted strings (never scientific notation), and nulls into "".
// encoding/json decodes every JSON number into float64, and fmt/html/template
// print floats with %g, which switches to scientific notation for large
// round numbers (e.g. unix timestamps) — exactly the kind of field a REST
// widget is likely to display.
func normalizeJSON(v any) any {
	switch val := v.(type) {
	case float64:
		if val == math.Trunc(val) && math.Abs(val) < 1e15 {
			return strconv.FormatInt(int64(val), 10)
		}
		return strconv.FormatFloat(val, 'f', -1, 64)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = normalizeJSON(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = normalizeJSON(vv)
		}
		return out
	case nil:
		return ""
	default:
		return val
	}
}

// ---- main -----------------------------------------------------------------

func main() {
	var settings map[string]string
	json.NewDecoder(os.Stdin).Decode(&settings)

	apiURL := strings.TrimSpace(settings["api_url"])
	tmplStr := settings["template"]
	method := strings.ToUpper(strings.TrimSpace(settings["http_method"]))
	if method == "" {
		method = "GET"
	}

	if apiURL == "" {
		fmt.Print(`<div class="rt-empty">Sin URL configurada</div>` + rtCSS)
		return
	}
	if tmplStr == "" {
		fmt.Print(`<div class="rt-empty">Sin plantilla configurada</div>` + rtCSS)
		return
	}

	var body string
	var ok bool
	switch {
	case method == "POST" && settings["auth_token"] != "":
		body, ok = httpPostAuth(apiURL, settings["request_body"], settings["auth_token"])
	case method == "POST":
		body, ok = httpPost(apiURL, settings["request_body"])
	default:
		body, ok = httpGet(apiURL)
	}
	if !ok {
		fmt.Print(`<div class="rt-error">No se pudo obtener datos de la API</div>` + rtCSS)
		return
	}

	var raw any
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		fmt.Print(`<div class="rt-error">Respuesta no es JSON válido</div>` + rtCSS)
		return
	}
	data := normalizeJSON(raw)

	tmpl, err := template.New("w").Option("missingkey=zero").Parse(tmplStr)
	if err != nil {
		fmt.Print(`<div class="rt-error">Error en la plantilla: ` + esc(err.Error()) + `</div>` + rtCSS)
		return
	}

	var sb strings.Builder
	sb.WriteString(`<div class="rt-wrap">`)
	if err := tmpl.Execute(&sb, data); err != nil {
		fmt.Print(`<div class="rt-error">Error al renderizar: ` + esc(err.Error()) + `</div>` + rtCSS)
		return
	}
	sb.WriteString(`</div>`)
	sb.WriteString(rtCSS)
	fmt.Print(sb.String())
}

const rtCSS = `<style>
.rt-wrap{font-size:.85rem;line-height:1.5}
.rt-empty{font-size:.8rem;color:rgba(255,255,255,.35)}
.rt-error{font-size:.78rem;color:#f87171;word-break:break-word}
</style>`
