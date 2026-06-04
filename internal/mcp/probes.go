package mcp

import "strings"

// probeContract is the per-language probe one-liner set returned by debug_start
// (D3). Every form is fire-and-forget (failures swallowed, never awaited) and
// NEVER sets a JSON Content-Type, so browser probes stay CORS "simple requests"
// with no preflight (D3). The skill substitutes a real probe ID for <probe> and
// fills the body with the values that discriminate the suspect.
type probeContract struct {
	URLTemplate string            `json:"url_template"` // .../log/<session>/<probe>
	Note        string            `json:"note"`         // the contract in one sentence
	OneLiners   map[string]string `json:"one_liners"`   // language → canonical probe line
}

// buildProbeContract renders the probe one-liners for a session's URL template.
// The template already carries the daemon's host:port (D4), so a SHERLOG_PORT
// override propagates into every emitted line automatically.
func buildProbeContract(urlTemplate string) probeContract {
	u := urlTemplate
	return probeContract{
		URLTemplate: u,
		Note: "Fire-and-forget: never await the call, never set a JSON Content-Type " +
			"(keeps browser probes preflight-free). Swap <probe> for the registered " +
			"probe ID and put discriminating values in the body.",
		OneLiners: map[string]string{
			// Browser / Node 18+: fetch with text/plain default body, errors swallowed.
			"js": `fetch("` + u + `", {method:"POST", body: JSON.stringify({/* values */})}).catch(() => {})`,
			// Python: stdlib urllib, no requests dependency assumed; daemon ignores type.
			"python": `import urllib.request, json` + "\n" +
				`try: urllib.request.urlopen(urllib.request.Request("` + u + `", data=json.dumps({}).encode()))` + "\n" +
				`except Exception: pass`,
			// Go: http.Post in a goroutine so it never blocks the host path. Empty
			// content-type arg keeps it text/plain.
			"go": `go func(){ if r, err := http.Post("` + u + `", "", strings.NewReader("{}")); err == nil { r.Body.Close() } }()`,
			// Ruby: Net::HTTP, rescued so a down daemon is silent.
			"ruby": `begin; require "net/http"; Net::HTTP.post(URI("` + u + `"), "{}"); rescue StandardError; end`,
			// curl: --data (text/plain), background, silent — shell repro scripts.
			"curl": `curl -s -X POST --data '{}' "` + u + `" >/dev/null 2>&1 &`,
		},
	}
}

// greppableFragment is the substring that uniquely identifies this session's
// probes in source (D10). debug_end returns it so the skill can grep the repo and
// require zero matches before declaring the case closed.
func greppableFragment(urlTemplate string) string {
	// Trim the "<probe>" tail to the per-session prefix .../log/<session>/, which
	// matches every probe line regardless of probe ID.
	if i := strings.Index(urlTemplate, "/<probe>"); i >= 0 {
		return urlTemplate[:i+len("/")] // include trailing slash up to <probe>
	}
	return urlTemplate
}
