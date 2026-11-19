// EGRESS RULES: what the guest may reach, and by which path.
//
// TWO QUESTIONS, ONE TABLE. A person configuring a guest's network has two things to say about a
// destination, and they are independent:
//
//	MAY it be reached at all?        allow  / block
//	BY WHICH PATH?                   proxy  / direct
//
// `block` is a refusal. `direct` is an exception to the proxy: the connection leaves this pod the way
// it would on a machine with an unfiltered route, which matters when the proxy is slower than the
// wire, when a destination is on the local network, or when a service refuses proxied traffic.
//
// WHY THIS IS NOT A FIREWALL AND MUST NOT BE MARKETED AS ONE. It matches on what a connection is
// addressed to, and a connection is addressed to an IP. A rule for `github.com` works because this
// daemon ALSO resolved that name moments earlier and remembered the answer - so a program that dials
// `140.82.121.4` without asking DNS is matched only if that address is in the table, and one that
// uses its own resolver is matched not at all. That is stated here because the alternative is a UI
// that implies a guarantee it cannot keep.
//
// THE TABLE IS RESOLVED, NOT EVALUATED. Every name is turned into addresses when the rules are
// applied and re-resolved on a timer, because the proxy sees addresses only. A name whose addresses
// all change and are not re-resolved stops matching, which is the failure mode this file exists to
// make small: `RefreshInterval` is five minutes, and a rule that has never resolved is reported as
// such rather than silently allowing everything.
package net

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// Verdict is what happens to one connection.
type Verdict int

const (
	// VerdictProxy sends the connection through the configured proxy. The default for a destination
	// no rule names, and the behaviour this daemon had before rules existed.
	VerdictProxy Verdict = iota
	// VerdictDirect leaves this pod without the proxy.
	VerdictDirect
	// VerdictBlock closes the connection.
	VerdictBlock
)

func (v Verdict) String() string {
	switch v {
	case VerdictDirect:
		return "direct"
	case VerdictBlock:
		return "block"
	default:
		return "proxy"
	}
}

// Policy is the whole configuration: a default for unmatched traffic, plus rules.
type Policy struct {
	// Default is what an unmatched destination gets. It is `proxy` unless somebody chose otherwise,
	// because that is what this daemon already did and a rules feature must not change behaviour until
	// it is asked to.
	Default Verdict
	// Rules are evaluated in order, first match wins.
	Rules []Rule
}

// Rule is one line of the table as a person wrote it.
//
// TWO KINDS, AND THE DIFFERENCE IS WHAT THEY CAN SEE. `address` rules are matched against the
// connection's destination address and need no DNS. `name` rules are matched against the names that
// address was resolved for, which this daemon learns from its own resolver (see dns.go) - so a
// `suffix:` rule covers every subdomain that resolved through it, and nothing that did not.
type Rule struct {
	// Match is the thing to match: a name for `name` rules, an address or CIDR for `address` ones.
	Match string `json:"match"`
	// Verdict is what to do when this rule matches.
	Verdict Verdict `json:"-"`
	// Action is the wire form: "proxy", "direct" or "block".
	Action string `json:"action"`
	// Ports limits the rule to specific destination ports. Empty means every port.
	Ports []int `json:"ports,omitempty"`
}

// egress is the process-wide table. A rules feature with no configuration is a table with no rules
// and a `proxy` default, which is exactly the behaviour before it existed.
var (
	egressMu  sync.RWMutex
	egress    = &resolvedPolicy{policy: Policy{Default: VerdictProxy}}
	egressGen uint64 // bumped on every apply, so a resolver knows its work is stale
)

// resolvedRule is a Rule with its names turned into addresses, or kept as a name pattern.
type resolvedRule struct {
	Verdict Verdict
	Ports   []int
	// Nets are the addresses this rule covers, for `address` rules AND for `name` rules that were
	// resolved at apply time. A name rule keeps BOTH: the addresses are what it can match before any
	// traffic resolves it, and the pattern is what it matches afterwards for subdomains.
	Nets []*net.IPNet
	// Name is the pattern for `name` rules: exact, suffix (".example.com") or keyword ("*ads*").
	Name string
	// Kind is "address" or "name".
	Kind string
	// Match is kept for reporting: the UI shows which names failed to resolve.
	Match string
}

type resolvedPolicy struct {
	policy Policy
	rules  []resolvedRule
	// errs is one entry per name that did not resolve, so the UI can say which.
	errs []string
	at   time.Time
}

// SetEgress installs a policy and resolves it.
//
// IT RESOLVES SYNCHRONOUSLY, because the caller is an HTTP handler whose response is the answer to
// "did this work" - and returning before the names resolved would mean the UI reports success for a
// rule that matches nothing.
func SetEgress(p Policy) error {
	if p.Default != VerdictProxy && p.Default != VerdictDirect && p.Default != VerdictBlock {
		return fmt.Errorf("default must be proxy, direct or block")
	}
	rp := resolvePolicy(p)
	egressMu.Lock()
	egress = rp
	egressGen++
	egressMu.Unlock()
	return nil
}

// Egress returns the policy in force, with the resolution outcome for each name.
func Egress() (Policy, []string, time.Time) {
	egressMu.RLock()
	defer egressMu.RUnlock()
	return egress.policy, append([]string(nil), egress.errs...), egress.at
}

// VerdictFor answers what to do with a connection to ip:port.
//
// THE LOOKUP IS UNDER A READ LOCK AND IS DELIBERATELY CHEAP: it runs once per connection, and a
// connection can arrive thousands of times a second. Keeping the resolved table immutable between
// applies is what makes that safe.
func VerdictFor(ip net.IP, port int) Verdict {
	// THE NAMES COME FROM THE RESOLVER'S OWN TABLE, read outside the policy lock so a slow sweep
	// cannot delay a connection. An address a guest reached without ever asking this resolver has no
	// names, and then only address rules can match - which is the honest boundary stated in the
	// package comment.
	var names []string
	if ip != nil {
		names = dnsNames.namesFor(ip.String())
	}

	egressMu.RLock()
	defer egressMu.RUnlock()
	for i := range egress.rules {
		r := &egress.rules[i]
		if len(r.Ports) > 0 && !containsPort(r.Ports, port) {
			continue
		}
		if r.Kind == "name" {
			for _, n := range names {
				if nameMatches(r.Name, n) {
					return r.Verdict
				}
			}
			continue
		}
		// "address" AND "name+address" BOTH CARRY NETS, and a name+address rule also matches by name
		// so a connection whose address moved still matches what the guest asked for.
		for _, n := range r.Nets {
			if n.Contains(ip) {
				return r.Verdict
			}
		}
		if r.Kind == "name+address" {
			for _, n := range names {
				if nameMatches(r.Name, n) {
					return r.Verdict
				}
			}
		}
	}
	return egress.policy.Default
}

// nameMatches applies one rule pattern to one observed name.
//
// THE THREE SHAPES AND NOTHING ELSE - no globs, no regex. A rule language that can express anything
// is a rule language somebody will get subtly wrong, and the three cases below are what "block this
// domain" means in practice:
//
//	.example.com    the domain and every subdomain
//	example.com     exactly this host
//	ads             any name containing it (stored without the asterisks a person typed)
func nameMatches(pattern, name string) bool {
	if pattern == "" || name == "" {
		return false
	}
	switch {
	case strings.HasPrefix(pattern, "."):
		// A SUFFIX RULE MATCHES THE DOMAIN ITSELF TOO, which is what `.example.com` means to
		// everybody who writes it.
		base := pattern[1:]
		return name == base || strings.HasSuffix(name, pattern)
	case strings.Contains(pattern, "."):
		return name == pattern
	default:
		return strings.Contains(name, pattern)
	}
}

func containsPort(ports []int, p int) bool {
	for _, x := range ports {
		if x == p {
			return true
		}
	}
	return false
}

// resolvePolicy classifies each rule and, for the ones that name a host, resolves it.
func resolvePolicy(p Policy) *resolvedPolicy {
	rp := &resolvedPolicy{policy: p, at: time.Now()}
	for _, rule := range p.Rules {
		match := strings.TrimSpace(rule.Match)
		if match == "" {
			continue
		}
		rr := resolvedRule{Verdict: rule.Verdict, Ports: rule.Ports, Match: match, Kind: "address"}

		// THE PATTERN DECIDES THE KIND, AND THE PATTERN IS WHAT A PERSON TYPES:
		//
		//	.example.com    every subdomain, and the domain itself
		//	*ads*           any name containing ads
		//	example.com     exactly that name (and, because it is also a host, its addresses)
		//	10.0.0.0/8      an address range
		//
		// A BARE NAME IS BOTH. It becomes a name rule (so subdomains that resolve later still match
		// through `.` rules it shares a suffix with - no, precisely: an exact name rule matches only
		// itself) AND its addresses are added, because the connection that is already in flight has
		// no name attached until this daemon's resolver has seen it.
		switch {
		case strings.HasPrefix(match, "."):
			rr.Kind = "name"
			rr.Name = strings.ToLower(match)
			rp.rules = append(rp.rules, rr)
			continue
		case strings.HasPrefix(match, "*") || strings.HasSuffix(match, "*"):
			rr.Kind = "name"
			rr.Name = strings.ToLower(strings.Trim(match, "*"))
			if rr.Name == "" {
				rp.errs = append(rp.errs, match+": empty keyword")
				continue
			}
			rp.rules = append(rp.rules, rr)
			continue
		}

		// A CIDR OR A BARE ADDRESS IS USED AS GIVEN. `net.ParseCIDR` accepts both `10.0.0.0/8` and,
		// with a full mask, a single address; a bare address gets /32 so one code path covers both.
		if strings.Contains(match, "/") {
			if _, n, err := net.ParseCIDR(match); err == nil {
				rr.Nets = []*net.IPNet{n}
			} else {
				rp.errs = append(rp.errs, fmt.Sprintf("%s: not a CIDR", match))
			}
		} else if ip := net.ParseIP(match); ip != nil {
			rr.Nets = []*net.IPNet{{IP: ip, Mask: net.CIDRMask(32, 32)}}
			if ip.To4() == nil {
				rr.Nets[0].Mask = net.CIDRMask(128, 128)
			}
		} else {
			addrs, err := net.LookupIP(match)
			if err != nil || len(addrs) == 0 {
				rp.errs = append(rp.errs, match+": did not resolve")
				rp.rules = append(rp.rules, rr) // kept, so the UI can show it
				continue
			}
			for _, a := range addrs {
				bits := 32
				if a.To4() == nil {
					bits = 128
					// IPv6 IS SKIPPED, AND THE REASON IS THE GUEST'S OWN STACK. The relay and the
					// proxy both work in IPv4; a v6 address in this table would be a rule that can
					// never match, which is worse than an absent one because it looks like coverage.
					continue
				}
				rr.Nets = append(rr.Nets, &net.IPNet{IP: a, Mask: net.CIDRMask(bits, bits)})
			}
			// AND IT IS ALSO AN EXACT NAME RULE, because a connection made after this resolver has
			// seen the answer has a name attached and should match by that name rather than by an
			// address that may already be stale.
			rr.Kind = "name+address"
			rr.Name = strings.ToLower(match)
		}
		rp.rules = append(rp.rules, rr)
	}
	return rp
}

// RefreshInterval is how often the names are resolved again.
//
// FIVE MINUTES IS A COMPROMISE, NOT A LAW. Shorter re-resolves more often than a DNS TTL warrants and
// costs a lookup per rule; longer lets a service that moved addresses slip out of its own rule. A
// rules UI that says "applied" while the address changed an hour ago is the failure this interval
// makes unlikely rather than impossible.
const RefreshInterval = 5 * time.Minute

// StartEgressRefresh re-resolves the table on a timer, and stops when done is closed.
func StartEgressRefresh(done <-chan struct{}) {
	go func() {
		t := time.NewTicker(RefreshInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				egressMu.RLock()
				p := egress.policy
				egressMu.RUnlock()
				if len(p.Rules) == 0 {
					continue
				}
				// THE GENERATION IS CHECKED AFTER RESOLVING, not before: a policy applied while the
				// lookups were in flight must win, or the refresh would install stale addresses over
				// a fresh configuration.
				gen := egressGen
				rp := resolvePolicy(p)
				egressMu.Lock()
				if egressGen == gen {
					egress = rp
				}
				egressMu.Unlock()
			}
		}
	}()
}

// EgressRulesEnabled reports whether the daemon was told to expose the rules API.
//
// IT IS OFF UNLESS ASKED FOR, because the rules are a policy a person builds by hand and an interface
// that appears unbidden invites somebody to cut their own guest off. `EASYVM_EGRESS_RULES=1`.
func EgressRulesEnabled() bool { return osGetenv("EASYVM_EGRESS_RULES") == "1" }
