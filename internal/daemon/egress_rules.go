package daemon

// RESTFUL ROUTES FOR THE EGRESS RULES.
//
// THE COLLECTION ENDPOINT ALREADY EXISTED and is kept unchanged - `GET`/`POST /api/egress` is what the
// dashboard's textarea uses, and a whole-policy `PUT` is the right shape for a person editing a block
// of text. What was missing is the one thing a REST client needs and a textarea does not: ADDRESSING A
// SINGLE RULE.
//
//	GET    /api/egress/rules            list, each with its id
//	POST   /api/egress/rules            create one
//	GET    /api/egress/rules/{id}       read one
//	PUT    /api/egress/rules/{id}       replace one
//	DELETE /api/egress/rules/{id}       delete one
//	GET    /api/egress/default          the verdict for unmatched traffic
//	PUT    /api/egress/default          set it
//
// THE ID IS DERIVED FROM THE RULE'S CONTENT, which has one consequence worth stating ONCE here because
// every client meets it: `PUT` changes the content, so it changes the identifier. The response carries
// the new id, and a client that keeps using the old one gets a 404 - which is honest, and better than
// an id that silently addresses a different rule after a reorder.
//
// IDS COME FROM CONTENT RATHER THAN FROM A COUNTER because a counter has to be stored, and this daemon's
// rules live in memory and are replaced whole by a `POST`. A stored counter would be reset by every
// whole-policy write, and a client holding an id from before would address a rule that had been
// renumbered underneath it.

import (
	"encoding/json"
	"net/http"
	"strings"

	easyvmnet "github.com/easydev-org/easyvm/internal/net"
)

// egressRoutes dispatches everything under /api/egress/.
//
// THE PREFIX IS REGISTERED ONCE and the rest is parsed here, rather than registering a pattern per id:
// Go's ServeMux has no path parameters, and a `HandleFunc("/api/egress/rules/", ...)` that then splits
// the remainder is the idiomatic shape for the stdlib.
func (d *Daemon) handleEgressRules(w http.ResponseWriter, r *http.Request) {
	if !easyvmnet.EgressRulesEnabled() {
		http.Error(w, "egress rules are not enabled (EASYVM_EGRESS_RULES=1)", http.StatusNotFound)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/egress/rules")
	rest = strings.Trim(rest, "/")

	// THE COLLECTION.
	if rest == "" {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, egressResponse{
				OK:    true,
				Rules: rulesToWire(easyvmnet.EgressRules()),
			})
		case http.MethodPost:
			var in egressRule
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeJSON(w, http.StatusBadRequest, egressResponse{Error: err.Error()})
				return
			}
			rule, err := wireToRule(in)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, egressResponse{Error: err.Error()})
				return
			}
			saved, err := easyvmnet.AddRule(rule)
			if err != nil {
				// A DUPLICATE IS 409: the request was well formed and the state says no. A validation
				// error is 400. The distinction is what lets a client retry one and not the other.
				writeJSON(w, http.StatusConflict, egressResponse{Error: err.Error()})
				return
			}
			w.Header().Set("Location", "/api/egress/rules/"+easyvmnet.RuleID(saved))
			writeJSON(w, http.StatusCreated, egressRule{
				ID: easyvmnet.RuleID(saved), Match: saved.Match,
				Action: verdictAction(saved.Verdict), Upstream: saved.Verdict.Upstream,
				Ports: saved.Ports,
			})
		default:
			w.Header().Set("Allow", "GET, POST")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
		return
	}

	// ONE RULE.
	id := rest
	switch r.Method {
	case http.MethodGet:
		for _, rule := range easyvmnet.EgressRules() {
			if easyvmnet.RuleID(rule) == id {
				writeJSON(w, http.StatusOK, egressRule{
					ID: id, Match: rule.Match, Action: verdictAction(rule.Verdict),
					Upstream: rule.Verdict.Upstream, Ports: rule.Ports,
				})
				return
			}
		}
		writeJSON(w, http.StatusNotFound, egressResponse{Error: "no rule with id " + id})

	case http.MethodPut:
		var in egressRule
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, egressResponse{Error: err.Error()})
			return
		}
		rule, err := wireToRule(in)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, egressResponse{Error: err.Error()})
			return
		}
		saved, err := easyvmnet.ReplaceRule(id, rule)
		if err != nil {
			status := http.StatusConflict
			if strings.HasPrefix(err.Error(), "no rule with id") {
				status = http.StatusNotFound
			}
			writeJSON(w, status, egressResponse{Error: err.Error()})
			return
		}
		// THE NEW ID IS IN THE BODY AND IN `Content-Location`, because changing the match changes the
		// identifier and a client must not have to guess that.
		newID := easyvmnet.RuleID(saved)
		w.Header().Set("Content-Location", "/api/egress/rules/"+newID)
		writeJSON(w, http.StatusOK, egressRule{
			ID: newID, Match: saved.Match, Action: verdictAction(saved.Verdict),
			Upstream: saved.Verdict.Upstream, Ports: saved.Ports,
		})

	case http.MethodDelete:
		existed, err := easyvmnet.DeleteRule(id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, egressResponse{Error: err.Error()})
			return
		}
		if !existed {
			writeJSON(w, http.StatusNotFound, egressResponse{Error: "no rule with id " + id})
			return
		}
		// 204: the rule is gone and there is nothing to say about it.
		w.WriteHeader(http.StatusNoContent)

	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleEgressDefault serves the verdict for destinations no rule names.
func (d *Daemon) handleEgressDefault(w http.ResponseWriter, r *http.Request) {
	if !easyvmnet.EgressRulesEnabled() {
		http.Error(w, "egress rules are not enabled (EASYVM_EGRESS_RULES=1)", http.StatusNotFound)
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, egressResponse{
			OK: true, Default: verdictAction(easyvmnet.DefaultVerdict()),
		})
	case http.MethodPut:
		var in struct {
			Default string `json:"default"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, egressResponse{Error: err.Error()})
			return
		}
		v, err := parseAction(in.Default)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, egressResponse{Error: err.Error()})
			return
		}
		if err := easyvmnet.SetDefault(v); err != nil {
			writeJSON(w, http.StatusBadRequest, egressResponse{Error: err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, egressResponse{OK: true, Default: verdictAction(v)})
	default:
		w.Header().Set("Allow", "GET, PUT")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// rulesToWire and wireToRule are the one place the internal shape and the wire shape meet.
func rulesToWire(rules []easyvmnet.Rule) []egressRule {
	out := make([]egressRule, 0, len(rules))
	for _, r := range rules {
		out = append(out, egressRule{
			ID: easyvmnet.RuleID(r), Match: r.Match, Action: verdictAction(r.Verdict),
			Upstream: r.Verdict.Upstream, Ports: r.Ports,
		})
	}
	return out
}

func wireToRule(in egressRule) (easyvmnet.Rule, error) {
	if strings.TrimSpace(in.Match) == "" {
		return easyvmnet.Rule{}, errString("match is required")
	}
	v, err := parseAction(in.Action)
	if err != nil {
		return easyvmnet.Rule{}, err
	}
	if in.Upstream != "" && v.Kind == easyvmnet.KindProxy && v.Upstream == "" {
		v.Upstream = in.Upstream
	}
	return easyvmnet.Rule{Match: in.Match, Verdict: v, Action: in.Action, Ports: in.Ports}, nil
}

// errString is a one-line error without importing fmt for it.
type errString string

func (e errString) Error() string { return string(e) }
