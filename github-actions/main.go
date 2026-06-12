//go:build wasip1

// GitHub Actions status module for LalaDashboard.
// Fetches the latest CI run for multiple repos in a single GraphQL request.
//
// Compile: GOOS=wasip1 GOARCH=wasm go build -o widget.wasm .
package main

import (
	"encoding/json"
	"fmt"
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

//go:wasmimport env http_post_auth
func hostHTTPPostAuth(urlPtr, urlLen, bodyPtr, bodyLen, authPtr, authLen, resultPtr uint32) uint32

var httpBuf [1 << 19]byte // 512 KB for GraphQL response

func httpPostAuth(rawURL, body, auth string) (string, bool) {
	urlB := []byte(rawURL)
	bodyB := []byte(body)
	authB := []byte(auth)
	if len(urlB) == 0 || len(bodyB) == 0 {
		return "", false
	}
	n := hostHTTPPostAuth(
		uint32(uintptr(unsafe.Pointer(&urlB[0]))), uint32(len(urlB)),
		uint32(uintptr(unsafe.Pointer(&bodyB[0]))), uint32(len(bodyB)),
		uint32(uintptr(unsafe.Pointer(&authB[0]))), uint32(len(authB)),
		uint32(uintptr(unsafe.Pointer(&httpBuf[0]))),
	)
	if n == 0 {
		return "", false
	}
	return string(httpBuf[:n]), true
}

// ---- module metadata ------------------------------------------------------

//export module_name
func moduleName() int32 {
	setOutput("GitHub Actions")
	return 0
}

//export config_schema
func configSchema() int32 {
	setOutput(`[
  {"key":"github_token","label":"GitHub Token","type":"text","required":true,"default":"","placeholder":"ghp_..."},
  {"key":"repos","label":"Repositorios (uno por línea, owner/repo)","type":"textarea","required":true,"default":"","placeholder":"Fiambre/laladashboard\nSelknam-Tech/Libra"},
  {"key":"poll_seconds","label":"Intervalo (segundos)","type":"number","default":"300"}
]`)
	return 0
}

// ---- GraphQL types --------------------------------------------------------

type gqlResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type repoData struct {
	NameWithOwner  string `json:"nameWithOwner"`
	DefaultBranchRef *struct {
		Target *struct {
			CheckSuites *struct {
				Nodes []checkSuiteNode `json:"nodes"`
			} `json:"checkSuites"`
		} `json:"target"`
	} `json:"defaultBranchRef"`
}

type checkSuiteNode struct {
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	UpdatedAt  string `json:"updatedAt"`
	WorkflowRun *struct {
		Workflow *struct {
			Name string `json:"name"`
		} `json:"workflow"`
		URL string `json:"url"`
	} `json:"workflowRun"`
}

type repoStatus struct {
	owner        string
	repo         string
	conclusion   string // SUCCESS, FAILURE, IN_PROGRESS, NEUTRAL, SKIPPED, ""
	workflowName string
	updatedAt    time.Time
	url          string
}

// ---- helpers ---------------------------------------------------------------

func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func statusIcon(conclusion, status string) (icon, class string) {
	if status == "IN_PROGRESS" || status == "QUEUED" || status == "WAITING" {
		return "◌", "gha-run"
	}
	switch conclusion {
	case "SUCCESS":
		return "✓", "gha-ok"
	case "FAILURE", "TIMED_OUT", "STARTUP_FAILURE":
		return "✗", "gha-fail"
	case "CANCELLED", "SKIPPED", "NEUTRAL", "ACTION_REQUIRED":
		return "—", "gha-skip"
	default:
		return "·", "gha-skip"
	}
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

	token := strings.TrimSpace(settings["github_token"])
	if token == "" {
		setOutput(`<div class="gha-error">Token de GitHub no configurado</div>` + ghaCSS)
		return 0
	}

	reposRaw := strings.TrimSpace(settings["repos"])
	if reposRaw == "" {
		setOutput(`<div class="gha-error">No hay repositorios configurados</div>` + ghaCSS)
		return 0
	}

	type repoRef struct{ owner, name string }
	var repos []repoRef
	for _, line := range strings.Split(reposRaw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "/", 2)
		if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
			repos = append(repos, repoRef{parts[0], parts[1]})
		}
	}
	if len(repos) == 0 {
		setOutput(`<div class="gha-error">Formato inválido. Use: owner/repo</div>` + ghaCSS)
		return 0
	}

	// Build GraphQL query with repo aliases
	var qb strings.Builder
	qb.WriteString("query {")
	for i, r := range repos {
		qb.WriteString(fmt.Sprintf(
			`r%d: repository(owner: %q, name: %q) {`+
				`nameWithOwner `+
				`defaultBranchRef { target { ... on Commit { `+
				`checkSuites(last: 1, filterBy: {appId: 15368}) {`+
				`nodes { status conclusion updatedAt `+
				`workflowRun { workflow { name } url } }}}}} }`,
			i, r.owner, r.name,
		))
	}
	qb.WriteString("}")

	queryJSON, _ := json.Marshal(map[string]string{"query": qb.String()})
	body := string(queryJSON)

	respStr, ok := httpPostAuth("https://api.github.com/graphql", body, token)
	if !ok {
		setOutput(`<div class="gha-error">Error conectando a GitHub API</div>` + ghaCSS)
		return 0
	}

	var gqlResp gqlResponse
	if err := json.Unmarshal([]byte(respStr), &gqlResp); err != nil {
		setOutput(`<div class="gha-error">Respuesta GraphQL inválida</div>` + ghaCSS)
		return 0
	}
	if len(gqlResp.Errors) > 0 {
		setOutput(`<div class="gha-error">` + esc(gqlResp.Errors[0].Message) + `</div>` + ghaCSS)
		return 0
	}

	// Parse results
	var statuses []repoStatus
	for i := range repos {
		alias := fmt.Sprintf("r%d", i)
		raw, ok := gqlResp.Data[alias]
		if !ok {
			continue
		}
		var rd repoData
		if err := json.Unmarshal(raw, &rd); err != nil {
			continue
		}
		nwo := rd.NameWithOwner
		parts := strings.SplitN(nwo, "/", 2)
		owner := ""
		if len(parts) == 2 {
			owner = parts[0]
		}

		rs := repoStatus{owner: owner, repo: nwo}

		if rd.DefaultBranchRef != nil &&
			rd.DefaultBranchRef.Target != nil &&
			rd.DefaultBranchRef.Target.CheckSuites != nil &&
			len(rd.DefaultBranchRef.Target.CheckSuites.Nodes) > 0 {
			node := rd.DefaultBranchRef.Target.CheckSuites.Nodes[0]
			rs.conclusion = node.Conclusion
			if rs.conclusion == "" {
				rs.conclusion = node.Status
			}
			if node.UpdatedAt != "" {
				rs.updatedAt, _ = time.Parse(time.RFC3339, node.UpdatedAt)
			}
			if node.WorkflowRun != nil {
				rs.url = node.WorkflowRun.URL
				if node.WorkflowRun.Workflow != nil {
					rs.workflowName = node.WorkflowRun.Workflow.Name
				}
			}
		}
		statuses = append(statuses, rs)
	}

	// Group by org
	type orgGroup struct {
		org      string
		statuses []repoStatus
	}
	seen := map[string]int{}
	var groups []orgGroup
	for _, s := range statuses {
		if idx, ok := seen[s.owner]; ok {
			groups[idx].statuses = append(groups[idx].statuses, s)
		} else {
			seen[s.owner] = len(groups)
			groups = append(groups, orgGroup{org: s.owner, statuses: []repoStatus{s}})
		}
	}

	var sb strings.Builder
	sb.WriteString(`<div class="gha-widget">`)

	multiOrg := len(groups) > 1
	for _, g := range groups {
		if multiOrg {
			sb.WriteString(`<div class="gha-org">` + esc(g.org) + `</div>`)
		}
		for _, s := range g.statuses {
			icon, cls := statusIcon(s.conclusion, s.conclusion)
			repoShort := s.repo
			if i := strings.Index(repoShort, "/"); i >= 0 {
				repoShort = repoShort[i+1:]
			}
			age := relTime(s.updatedAt)

			sb.WriteString(`<div class="gha-row">`)
			sb.WriteString(fmt.Sprintf(`<span class="gha-icon %s">%s</span>`, cls, icon))
			sb.WriteString(fmt.Sprintf(`<span class="gha-repo" title="%s">%s</span>`, esc(s.repo), esc(repoShort)))
			sb.WriteString(fmt.Sprintf(`<span class="gha-wf">%s</span>`, esc(s.workflowName)))
			sb.WriteString(fmt.Sprintf(`<span class="gha-age">%s</span>`, esc(age)))
			sb.WriteString(`</div>`)
		}
	}

	sb.WriteString(`</div>`)
	sb.WriteString(ghaCSS)
	setOutput(sb.String())
	return 0
}

const ghaCSS = `<style>
.gha-widget{display:flex;flex-direction:column;gap:.18rem}
.gha-error{font-size:.78rem;color:#f87171;padding:.4rem 0}
.gha-org{font-size:.58rem;color:rgba(255,255,255,.32);letter-spacing:.12em;text-transform:uppercase;border-bottom:1px solid rgba(255,255,255,.07);margin:.45rem 0 .2rem;padding-bottom:.15rem}
.gha-row{display:grid;grid-template-columns:14px 1fr 1fr auto;align-items:center;gap:.45rem;padding:.08rem 0}
.gha-icon{font-size:.75rem;font-weight:700;text-align:center;line-height:1}
.gha-ok{color:#4ade80}
.gha-fail{color:#f87171}
.gha-run{color:#facc15;display:inline-block;animation:gha-spin 1.8s linear infinite}
.gha-skip{color:rgba(255,255,255,.28)}
@keyframes gha-spin{to{transform:rotate(360deg)}}
.gha-repo{font-size:.74rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.gha-wf{font-size:.68rem;color:rgba(255,255,255,.35);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.gha-age{font-size:.66rem;color:rgba(255,255,255,.28);white-space:nowrap;text-align:right}
</style>`
