//go:build wasip1

// example-hello: módulo de ejemplo para LalaDashboard.
// Compile: GOOS=wasip1 GOARCH=wasm go build -o widget.wasm .
package main

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

func main() {}

var outBuf [1 << 20]byte
var outLen int32

func setOutput(s string) { n := copy(outBuf[:], s); outLen = int32(n) }

//export get_output_ptr
func getOutputPtr() int32 { return int32(uintptr(unsafe.Pointer(&outBuf[0]))) }

//export get_output_len
func getOutputLen() int32 { return outLen }

//export alloc
func alloc(size uint32) uint32 {
	b := make([]byte, size)
	return uint32(uintptr(unsafe.Pointer(&b[0])))
}

//export module_name
func moduleName() int32 {
	setOutput("Hello World")
	return 0
}

//export config_schema
func configSchema() int32 {
	schema := `[
		{"key":"message","label":"Mensaje","type":"text","default":"Hey there!"},
		{"key":"emoji","label":"Emoji","type":"text","default":"👋"},
		{"key":"poll_seconds","label":"Actualizar (seg)","type":"number","default":"0"}
	]`
	setOutput(schema)
	return 0
}

//export render
func render(cfgPtr, cfgLen uint32) int32 {
	cfgBytes := make([]byte, cfgLen)
	for i := uint32(0); i < cfgLen; i++ {
		cfgBytes[i] = *(*byte)(unsafe.Pointer(uintptr(cfgPtr) + uintptr(i)))
	}
	var settings map[string]string
	json.Unmarshal(cfgBytes, &settings)

	msg := settings["message"]
	if msg == "" {
		msg = "Hey there!"
	}
	emoji := settings["emoji"]
	if emoji == "" {
		emoji = "👋"
	}

	html := fmt.Sprintf(`<div style="text-align:center;padding:1.5rem;">
		<div style="font-size:3rem;line-height:1;margin-bottom:0.5rem;">%s</div>
		<div style="font-size:2rem;font-weight:100;letter-spacing:-1px;">%s</div>
	</div>`, emoji, msg)

	setOutput(html)
	return 0
}
