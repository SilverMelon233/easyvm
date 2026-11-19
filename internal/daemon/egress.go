package daemon

// THE EGRESS RULES, AS AN HTTP RESOURCE.
//
// THE RULES ARE NOT PER-MACHINE, AND THAT IS A DELIBERATE SIMPLIFICATION. There is one guest per
// container (`net.GuestIP` is a constant), so "this machine's rules" and "this daemon's rules" are the
// same sentence. A per-machine key would be an abstraction with exactly one possible value, and the
// day a second guest exists is the day the rules table needs a redesign anyway - not the day a map
// with one entry would have saved us.
//
// IT IS BEHIND `EASYVM_EGRESS_RULES=1`. A policy interface that appears unbidden invites somebody to
// cut their own guest off and then wonder why `apt` stopped working; the variable is what makes it a
// decision somebody made.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	easyvmnet "github.com/easydev-org/easyvm/internal/net"
)

// egressRequest is the wire form of a policy. It is a LIST OF LINES rather than a nested structure,
// because that is how a person thinks about it: "this name does this".
type egressRequest struct {
	// Default is "proxy", "direct" or "block" for anything no rule matches.
	Default string `json:"default"`
	// DefaultUpstream names the upstream an unnamed "proxy" uses. Empty means the one this daemon
	// was started with.
	DefaultUpstream string `json:"default_upstream,omitempty"`
	// Rules are evaluated in order; the first match wins.
	Rules []egressRule `json:"rules"`
	// Upstreams replaces the table. Omitted means "leave it alone", which is what a UI that only
	// edits rules sends.
	Upstreams []upstreamJSON `json:"upstreams,omitempty"`
}

// upstreamJSON is one proxy a rule can name.
type upstreamJSON struct {
	Name string `json:"name"`
	// HTTP is the CONNECT proxy (TCP), SOCKS5 the UDP relay. Either may be empty.
	HTTP   string `json:"http,omitempty"`
	SOCKS5 string `json:"socks5,omitempty"`
}

type egressRule struct {
	// Match is a domain, a CIDR or a single address.
	Match string `json:"match"`
	// Action is "proxy", "direct" or "block".
	Action string `json:"action"`
	// Upstream names which proxy, for action "proxy". Empty means the default.
	Upstream string `json:"upstream,omitempty"`
	// Ports restricts the rule to those destination ports.
	Ports []int `json:"ports,omitempty"`
}

type egressResponse struct {
	OK bool `json:"ok"`
	// Default and Rules are what is in force now.
	Default         string         `json:"default"`
	DefaultUpstream string         `json:"default_upstream,omitempty"`
	Rules           []egressRule   `json:"rules"`
	Upstreams       []upstreamJSON `json:"upstreams,omitempty"`
	// Unresolved names the rules whose names could not be turned into addresses. THEY ARE SHOWN
	// RATHER THAN FAILING THE REQUEST: a typo in one line should not discard the other nine, and a
	// rule that matches nothing is visible here instead of behaving like a silent allow.
	Unresolved []string `json:"unresolved,omitempty"`
	// AppliedAt is when the names were last resolved.
	AppliedAt string `json:"applied_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

// parseAction turns one wire word into a verdict.
//
// TWO FORMS: `proxy` and `proxy:NAME`. The colon is the whole syntax for naming an upstream, because
// a separate field would let a caller send `action: "direct", upstream: "fast"` and mean nothing -
// this way the illegal combination cannot be written.
func parseAction(s string) (easyvmnet.Verdict, error) {
	switch {
	case s == "direct":
		return easyvmnet.Verdict{Kind: easyvmnet.KindDirect}, nil
	case s == "block":
		return easyvmnet.Verdict{Kind: easyvmnet.KindBlock}, nil
	case s == "proxy":
		return easyvmnet.Verdict{Kind: easyvmnet.KindProxy}, nil
	case strings.HasPrefix(s, "proxy:"):
		name := strings.TrimPrefix(s, "proxy:")
		if name == "" {
			return easyvmnet.Verdict{}, fmt.Errorf("proxy: needs an upstream name")
		}
		return easyvmnet.Verdict{Kind: easyvmnet.KindProxy, Upstream: name}, nil
	}
	return easyvmnet.Verdict{}, fmt.Errorf("action must be proxy, proxy:<name>, direct or block")
}

func verdictAction(v easyvmnet.Verdict) string { return v.String() }

// handleEgress serves GET (read) and POST (replace).
func (d *Daemon) handleEgress(w http.ResponseWriter, r *http.Request) {
	if !easyvmnet.EgressRulesEnabled() {
		http.Error(w, "egress rules are not enabled (EASYVM_EGRESS_RULES=1)", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		d.writeEgress(w)
	case http.MethodPost:
		var req egressRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, egressResponse{Error: err.Error()})
			return
		}
		// THE UPSTREAM TABLE GOES IN FIRST, so the rules that name an upstream can be validated
		// against it in the same request - one POST configures both, and a rule naming a table entry
		// that has not been created yet is refused rather than silently doing nothing.
		if len(req.Upstreams) > 0 {
			list := make([]easyvmnet.Upstream, 0, len(req.Upstreams))
			for _, u := range req.Upstreams {
				list = append(list, easyvmnet.Upstream{Name: u.Name, HTTP: u.HTTP, SOCKS5: u.SOCKS5})
			}
			if err := easyvmnet.SetUpstreams(list); err != nil {
				writeJSON(w, http.StatusBadRequest, egressResponse{Error: err.Error()})
				return
			}
		}

		def := easyvmnet.Verdict{Kind: easyvmnet.KindProxy}
		if req.Default != "" {
			v, err := parseAction(req.Default)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, egressResponse{Error: "default: " + err.Error()})
				return
			}
			def = v
		}
		if req.DefaultUpstream != "" && def.Kind == easyvmnet.KindProxy {
			def.Upstream = req.DefaultUpstream
		}
		policy := easyvmnet.Policy{Default: def}
		for _, rule := range req.Rules {
			v, err := parseAction(rule.Action)
			if err != nil {
				writeJSON(w, http.StatusBadRequest,
					egressResponse{Error: rule.Match + ": " + err.Error()})
				return
			}
			// A RULE MAY NAME AN UPSTREAM DIRECTLY, which is the accepted shorthand; the action word
			// wins when both are present.
			if rule.Upstream != "" && v.Kind == easyvmnet.KindProxy && v.Upstream == "" {
				v.Upstream = rule.Upstream
			}
			policy.Rules = append(policy.Rules, easyvmnet.Rule{
				Match: rule.Match, Verdict: v, Action: rule.Action, Ports: rule.Ports,
			})
		}
		if err := easyvmnet.SetEgress(policy); err != nil {
			writeJSON(w, http.StatusBadRequest, egressResponse{Error: err.Error()})
			return
		}
		d.writeEgress(w)
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (d *Daemon) writeEgress(w http.ResponseWriter) {
	policy, unresolved, at := easyvmnet.Egress()
	out := egressResponse{
		OK:              true,
		Default:         verdictAction(policy.Default),
		DefaultUpstream: policy.Default.Upstream,
		Unresolved:      unresolved,
	}
	for _, u := range easyvmnet.UpstreamsCopy() {
		out.Upstreams = append(out.Upstreams, upstreamJSON{Name: u.Name, HTTP: u.HTTP, SOCKS5: u.SOCKS5})
	}
	if !at.IsZero() {
		out.AppliedAt = at.UTC().Format("2006-01-02T15:04:05Z")
	}
	for _, rule := range policy.Rules {
		out.Rules = append(out.Rules, egressRule{
			Match: rule.Match, Action: verdictAction(rule.Verdict),
			Upstream: rule.Verdict.Upstream, Ports: rule.Ports,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
