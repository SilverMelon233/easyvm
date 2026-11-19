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
	"net/http"

	easyvmnet "github.com/easydev-org/easyvm/internal/net"
)

// egressRequest is the wire form of a policy. It is a LIST OF LINES rather than a nested structure,
// because that is how a person thinks about it: "this name does this".
type egressRequest struct {
	// Default is "proxy", "direct" or "block" for anything no rule matches.
	Default string `json:"default"`
	// Rules are evaluated in order; the first match wins.
	Rules []egressRule `json:"rules"`
}

type egressRule struct {
	// Match is a domain, a CIDR or a single address.
	Match string `json:"match"`
	// Action is "proxy", "direct" or "block".
	Action string `json:"action"`
	// Ports restricts the rule to those destination ports.
	Ports []int `json:"ports,omitempty"`
}

type egressResponse struct {
	OK bool `json:"ok"`
	// Default and Rules are what is in force now.
	Default string       `json:"default"`
	Rules   []egressRule `json:"rules"`
	// Unresolved names the rules whose names could not be turned into addresses. THEY ARE SHOWN
	// RATHER THAN FAILING THE REQUEST: a typo in one line should not discard the other nine, and a
	// rule that matches nothing is visible here instead of behaving like a silent allow.
	Unresolved []string `json:"unresolved,omitempty"`
	// AppliedAt is when the names were last resolved.
	AppliedAt string `json:"applied_at,omitempty"`
	Error     string `json:"error,omitempty"`
}

var actionVerdict = map[string]easyvmnet.Verdict{
	"proxy":  easyvmnet.VerdictProxy,
	"direct": easyvmnet.VerdictDirect,
	"block":  easyvmnet.VerdictBlock,
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
		def := easyvmnet.VerdictProxy
		if req.Default != "" {
			v, ok := actionVerdict[req.Default]
			if !ok {
				writeJSON(w, http.StatusBadRequest,
					egressResponse{Error: "default must be proxy, direct or block"})
				return
			}
			def = v
		}
		policy := easyvmnet.Policy{Default: def}
		for _, rule := range req.Rules {
			v, ok := actionVerdict[rule.Action]
			if !ok {
				writeJSON(w, http.StatusBadRequest,
					egressResponse{Error: rule.Match + ": action must be proxy, direct or block"})
				return
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
		OK:         true,
		Default:    verdictAction(policy.Default),
		Unresolved: unresolved,
	}
	if !at.IsZero() {
		out.AppliedAt = at.UTC().Format("2006-01-02T15:04:05Z")
	}
	for _, rule := range policy.Rules {
		out.Rules = append(out.Rules, egressRule{
			Match: rule.Match, Action: verdictAction(rule.Verdict), Ports: rule.Ports,
		})
	}
	writeJSON(w, http.StatusOK, out)
}
