//go:build wasip1

// GitHub Actions status module for LalaDashboard.
// Reads config JSON from stdin, writes rendered HTML to stdout.
//
// Compile: GOOS=wasip1 GOARCH=wasm go build -o widget.wasm .
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

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

// ---- GraphQL types --------------------------------------------------------

type gqlResponse struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type repoData struct {
	NameWithOwner    string `json:"nameWithOwner"`
	DefaultBranchRef *struct {
		Target *struct {
			CheckSuites *struct {
				Nodes []checkSuiteNode `json:"nodes"`
			} `json:"checkSuites"`
		} `json:"target"`
	} `json:"defaultBranchRef"`
}

type checkSuiteNode struct {
	Status      string `json:"status"`
	Conclusion  string `json:"conclusion"`
	WorkflowRun *struct {
		CreatedAt string `json:"createdAt"`
		Workflow  *struct {
			Name string `json:"name"`
		} `json:"workflow"`
	} `json:"workflowRun"`
}

type runEntry struct {
	nameWithOwner string
	conclusion    string
	status        string
	workflowName  string
	updatedAt     time.Time
}

// ---- helpers ---------------------------------------------------------------

func esc(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&#34;")
	return s
}

func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, _ = time.Parse(time.RFC3339, s)
	}
	return t
}

func relTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return "ahora"
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
	case "CANCELLED":
		return "✕", "gha-cancel"
	default:
		return "—", "gha-skip"
	}
}

// ---- main -----------------------------------------------------------------

func main() {
	var settings map[string]string
	json.NewDecoder(os.Stdin).Decode(&settings)

	token := strings.TrimSpace(settings["github_token"])
	if token == "" {
		fmt.Print(`<div class="gha-error">Token de GitHub no configurado</div>` + ghaCSS)
		return
	}

	reposRaw := strings.TrimSpace(settings["repos"])
	if reposRaw == "" {
		fmt.Print(`<div class="gha-error">No hay repositorios configurados</div>` + ghaCSS)
		return
	}

	maxRuns := 20
	if v, _ := strconv.Atoi(strings.TrimSpace(settings["max_runs"])); v > 0 {
		maxRuns = v
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
		fmt.Print(`<div class="gha-error">Formato inválido. Use: owner/repo</div>` + ghaCSS)
		return
	}

	// Build single GraphQL query with all repos aliased
	var qb strings.Builder
	qb.WriteString("query { rateLimit { cost remaining }")
	for i, r := range repos {
		qb.WriteString(fmt.Sprintf(
			`r%d: repository(owner: %q, name: %q) {`+
				`nameWithOwner `+
				`defaultBranchRef { target { ... on Commit { `+
				`checkSuites(last: 5, filterBy: {appId: 15368}) {`+
				`nodes { status conclusion `+
				`workflowRun { createdAt workflow { name } } }}}}} }`,
			i, r.owner, r.name,
		))
	}
	qb.WriteString("}")

	queryJSON, _ := json.Marshal(map[string]string{"query": qb.String()})
	respStr, ok := httpPostAuth("https://api.github.com/graphql", string(queryJSON), token)
	if !ok {
		fmt.Print(`<div class="gha-error">Error conectando a GitHub API</div>` + ghaCSS)
		return
	}

	var gqlResp gqlResponse
	if err := json.Unmarshal([]byte(respStr), &gqlResp); err != nil {
		fmt.Print(`<div class="gha-error">Respuesta GraphQL inválida</div>` + ghaCSS)
		return
	}
	// Log errors but don't abort — GraphQL returns partial data even when some repos fail
	for _, e := range gqlResp.Errors {
		fmt.Fprintf(os.Stderr, "[github-actions] graphql error: %s\n", e.Message)
	}
	if len(gqlResp.Data) == 0 {
		msg := "Error conectando a GitHub API"
		if len(gqlResp.Errors) > 0 {
			msg = gqlResp.Errors[0].Message
		}
		fmt.Print(`<div class="gha-error">` + esc(msg) + `</div>` + ghaCSS)
		return
	}
	if raw, ok := gqlResp.Data["rateLimit"]; ok {
		var rl struct {
			Cost      int `json:"cost"`
			Remaining int `json:"remaining"`
		}
		if err := json.Unmarshal(raw, &rl); err == nil {
			fmt.Fprintf(os.Stderr, "[github-actions] rateLimit cost=%d remaining=%d\n", rl.Cost, rl.Remaining)
		}
	}

	// Collect all run entries from all repos into a flat list
	var entries []runEntry
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
		if rd.DefaultBranchRef == nil ||
			rd.DefaultBranchRef.Target == nil ||
			rd.DefaultBranchRef.Target.CheckSuites == nil {
			continue
		}
		for _, node := range rd.DefaultBranchRef.Target.CheckSuites.Nodes {
			// Skip nodes with no associated workflow run (no timestamp or name)
			if node.WorkflowRun == nil || node.WorkflowRun.Workflow == nil {
				continue
			}
			t := parseTime(node.WorkflowRun.CreatedAt)
			if t.IsZero() {
				continue
			}
			e := runEntry{
				nameWithOwner: rd.NameWithOwner,
				conclusion:    node.Conclusion,
				status:        node.Status,
				workflowName:  node.WorkflowRun.Workflow.Name,
				updatedAt:     t,
			}
			entries = append(entries, e)
		}
	}

	// Sort by most recent first, limit to maxRuns
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].updatedAt.After(entries[j].updatedAt)
	})
	if len(entries) > maxRuns {
		entries = entries[:maxRuns]
	}

	var sb strings.Builder
	sb.WriteString(`<div class="gha-widget">`)

	for _, e := range entries {
		icon, cls := statusIcon(e.conclusion, e.status)
		age := relTime(e.updatedAt)

		sb.WriteString(`<div class="gha-row">`)
		sb.WriteString(fmt.Sprintf(`<span class="gha-icon %s">%s</span>`, cls, icon))
		sb.WriteString(fmt.Sprintf(`<span class="gha-repo" title="%s">%s</span>`, esc(e.nameWithOwner), esc(e.nameWithOwner)))
		sb.WriteString(fmt.Sprintf(`<span class="gha-wf">%s</span>`, esc(e.workflowName)))
		sb.WriteString(fmt.Sprintf(`<span class="gha-age">%s</span>`, esc(age)))
		sb.WriteString(`</div>`)
	}

	if len(entries) == 0 {
		sb.WriteString(`<div class="gha-empty">Sin ejecuciones recientes</div>`)
	}

	sb.WriteString(`</div>`)
	sb.WriteString(ghaCSS)
	fmt.Print(sb.String())
}

const ghaCSS = `<style>
.gha-widget{display:flex;flex-direction:column;gap:.18rem}
.gha-error,.gha-empty{font-size:.78rem;color:rgba(255,255,255,.4);padding:.4rem 0}
.gha-error{color:#f87171}
.gha-row{display:grid;grid-template-columns:14px 1fr 1fr auto;align-items:center;gap:.45rem;padding:.08rem 0}
.gha-icon{font-size:.75rem;font-weight:700;text-align:center;line-height:1}
.gha-ok{color:#4ade80}
.gha-fail{color:#f87171}
.gha-cancel{color:#fb923c}
.gha-run{color:#facc15;display:inline-block;animation:gha-spin 1.8s linear infinite}
.gha-skip{color:rgba(255,255,255,.28)}
@keyframes gha-spin{to{transform:rotate(360deg)}}
.gha-repo{font-size:.74rem;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.gha-wf{font-size:.68rem;color:rgba(255,255,255,.35);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.gha-age{font-size:.66rem;color:rgba(255,255,255,.28);white-space:nowrap;text-align:right}
</style>`
