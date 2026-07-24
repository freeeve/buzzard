// Rule packs: the classification rules as data. A pack is JSON describing
// match gates and ordered evidence variants; the first variant whose
// evidence all holds claims the directory. Rules stay explainable -- when a
// variant gives no why text, one is composed from the evidence that
// actually matched.
package rules

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
)

//go:embed default_pack.json
var defaultPackJSON []byte

// Pack is the top-level rule pack document.
type Pack struct {
	Rules []PackRule `json:"rules"`
}

// PackRule gates a set of variants behind a match condition.
type PackRule struct {
	Match    MatchSpec `json:"match"`
	Variants []Variant `json:"variants"`
}

// MatchSpec decides whether a rule applies to a directory: by basename, by
// a file the directory contains, or by a fixed path under the home
// directory whose purgeability is a platform contract.
type MatchSpec struct {
	Basenames   []string `json:"basenames,omitempty"`
	ContainsAny []string `json:"contains_any,omitempty"`
	HomePath    string   `json:"home_path,omitempty"`
}

// Variant is one evidence-graded claim a rule can make. Category defaults
// to the matched basename (with CategoryPrefix prepended); Why defaults to
// a description composed from the evidence that matched.
type Variant struct {
	Category       string         `json:"category,omitempty"`
	CategoryPrefix string         `json:"category_prefix,omitempty"`
	Tier           string         `json:"tier"`
	Regen          string         `json:"regen"`
	Why            string         `json:"why,omitempty"`
	Evidence       []EvidenceSpec `json:"evidence"`
}

// EvidenceSpec is one required piece of proof: a file that must exist
// beside the directory or inside it. Any listed name satisfies the spec.
type EvidenceSpec struct {
	SiblingAny  []string `json:"sibling_any,omitempty"`
	ContainsAny []string `json:"contains_any,omitempty"`
}

type compiledVariant struct {
	category string
	prefix   string
	tier     Tier
	regen    string
	why      string
	evidence []EvidenceSpec
}

type compiledRule struct {
	gate     MatchSpec
	variants []compiledVariant
}

// Compile validates a pack and resolves it against a home directory into a
// Ruleset ready to classify.
func Compile(p *Pack, home string) (*Ruleset, error) {
	rs := &Ruleset{
		byBase: make(map[string][]*compiledRule),
		fixed:  make(map[string]*Match),
	}
	for i, pr := range p.Rules {
		if err := validateRule(&pr); err != nil {
			return nil, fmt.Errorf("rule %d: %w", i, err)
		}
		if pr.Match.HomePath != "" {
			v := pr.Variants[0]
			tier, _ := parseTier(v.Tier)
			rs.fixed[filepath.Join(home, filepath.FromSlash(pr.Match.HomePath))] = &Match{
				Category: v.Category, Tier: tier, Evidence: v.Why, Regen: v.Regen,
			}
			continue
		}
		cr := &compiledRule{gate: pr.Match}
		for _, v := range pr.Variants {
			tier, _ := parseTier(v.Tier)
			cr.variants = append(cr.variants, compiledVariant{
				category: v.Category, prefix: v.CategoryPrefix, tier: tier,
				regen: v.Regen, why: v.Why, evidence: v.Evidence,
			})
		}
		if len(pr.Match.Basenames) > 0 {
			for _, b := range pr.Match.Basenames {
				rs.byBase[b] = append(rs.byBase[b], cr)
			}
		} else {
			rs.gated = append(rs.gated, cr)
		}
	}
	return rs, nil
}

// validateRule rejects rules that could not be evaluated or explained.
func validateRule(pr *PackRule) error {
	m := pr.Match
	gates := 0
	if len(m.Basenames) > 0 {
		gates++
	}
	if len(m.ContainsAny) > 0 {
		gates++
	}
	if m.HomePath != "" {
		gates++
	}
	if gates != 1 {
		return fmt.Errorf("match must set exactly one of basenames, contains_any, home_path")
	}
	if len(pr.Variants) == 0 {
		return fmt.Errorf("rule has no variants")
	}
	if m.HomePath != "" {
		if len(pr.Variants) != 1 {
			return fmt.Errorf("home_path rules take exactly one variant")
		}
		v := pr.Variants[0]
		if v.Category == "" || v.Why == "" {
			return fmt.Errorf("home_path variant needs category and why")
		}
	}
	for j, v := range pr.Variants {
		if _, err := parseTier(v.Tier); err != nil {
			return fmt.Errorf("variant %d: %w", j, err)
		}
		if v.Regen == "" {
			return fmt.Errorf("variant %d: regen is required", j)
		}
		if len(v.Evidence) == 0 && v.Why == "" {
			return fmt.Errorf("variant %d: needs evidence or an explicit why", j)
		}
	}
	return nil
}

// parseTier maps a pack tier string to its Tier.
func parseTier(s string) (Tier, error) {
	switch s {
	case "A", "a":
		return TierA, nil
	case "B", "b":
		return TierB, nil
	}
	return TierB, fmt.Errorf("unknown tier %q", s)
}

// eval tries a rule's variants in order and returns the first claim whose
// evidence all holds.
func (cr *compiledRule) eval(dir, base string) *Match {
	parent := filepath.Dir(dir)
	for i := range cr.variants {
		v := &cr.variants[i]
		var whyParts []string
		ok := true
		for _, ev := range v.evidence {
			part, hit := evalEvidence(dir, parent, &ev)
			if !hit {
				ok = false
				break
			}
			whyParts = append(whyParts, part)
		}
		if !ok {
			continue
		}
		category := v.category
		if category == "" {
			category = v.prefix + base
		}
		why := v.why
		if why == "" {
			why = strings.Join(whyParts, " + ")
		}
		return &Match{Category: category, Tier: v.tier, Evidence: why, Regen: v.regen}
	}
	return nil
}

// evalEvidence checks one spec, returning a human description of what
// matched.
func evalEvidence(dir, parent string, ev *EvidenceSpec) (string, bool) {
	if len(ev.SiblingAny) > 0 {
		if name, ok := anyExists(parent, ev.SiblingAny...); ok {
			return "sibling " + name, true
		}
		return "", false
	}
	if name, ok := anyExists(dir, ev.ContainsAny...); ok {
		return name + " inside", true
	}
	return "", false
}
