package config

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

//
// Filter strings: [!]<target>.[<generator>.]<operator>.<value>
//
//   3-part: target.op.value         (generator omitted = self / string compare)
//   4-part: target.generator.op.value
//
// Target field on the document; "" or "*" = wildcard over all fields.
// Generator derives a value from the field (len, regex, contains, …).
// Operator eq | ne | gt | gte | lt | lte.
// Value literal compared against generator output.

// Generator names a value-derivation function the filter applies to
// the document field before comparison (e.g. len reduces a string to
// its length; regex returns a match boolean).
type Generator string

const (
	GenSelf     Generator = ""         // identity (string compare)
	GenLen      Generator = "len"      // returns int (length)
	GenSim      Generator = "sim"      // returns float (semantic similarity); needs inference
	GenRegex    Generator = "regex"    // returns bool (matches regex)
	GenContains Generator = "contains" // returns bool (substring match)
	GenCount    Generator = "count"    // returns int (array length; needs typed Doc)
	GenEmpty    Generator = "empty"    // returns bool (field is empty)
)

// Operator is the comparison applied between the Generator's output
// and the configured Value (eq / ne / gt / gte / lt / lte).
type Operator string

const (
	OpEq  Operator = "eq"
	OpNe  Operator = "ne"
	OpGt  Operator = "gt"
	OpGte Operator = "gte"
	OpLt  Operator = "lt"
	OpLte Operator = "lte"
)

// Filter one parsed condition.
type Filter struct {
	Negate    bool
	Target    string
	Generator Generator
	Operator  Operator
	Value     string
}

// Doc anything the evaluator can pull fields from. Article satisfies
// this via a thin adapter when filters are wired into the pipeline.
type Doc interface {
	Field(name string) (string, bool)
	Fields() []string // used to expand "*" wildcard targets
}

// ParseFilter split on first 3 dots; bind to Filter. Bad operator /
// generator surfaces at config load, not at first article.
func ParseFilter(s string) (Filter, error) {
	var f Filter

	if strings.HasPrefix(s, "!") {
		f.Negate = true
		s = s[1:]
	}

	// SplitN cap = 4 so values may contain dots (score.gte.0.85 works).
	parts := strings.SplitN(s, ".", 4)

	switch len(parts) {
	case 3:
		// Two shapes share this length:
		//   target.op.value           (generator omitted = self)
		//   gen.op.value              (target omitted = wildcard "*")
		// Disambiguate by checking whether parts[0] is a known
		// generator. Matches the most common config form like
		// `len.gte.100` without forcing the operator to spell out a
		// target.
		if validGenerator(Generator(parts[0])) && parts[0] != string(GenSelf) {
			f.Target = "*"
			f.Generator = Generator(parts[0])
			f.Operator = Operator(parts[1])
			f.Value = parts[2]
			break
		}
		f.Target, f.Operator, f.Value = parts[0], Operator(parts[1]), parts[2]
		f.Generator = GenSelf
	case 4:
		// Disambiguate 3-part-with-dotted-value vs true 4-part. If parts[1]
		// is a valid operator the form is target.op.value where value
		// contains a dot (e.g. score.gte.0.85). Otherwise parts[1] is a
		// generator and the form is target.generator.op.value.
		if validOperator(Operator(parts[1])) {
			f.Target = parts[0]
			f.Operator = Operator(parts[1])
			f.Value = parts[2] + "." + parts[3]
			f.Generator = GenSelf
		} else {
			f.Target = parts[0]
			f.Generator = Generator(parts[1])
			f.Operator = Operator(parts[2])
			f.Value = parts[3]
		}
	default:
		return f, fmt.Errorf("bad filter %q: need target.[generator.]op.value", s)
	}

	if f.Target == "" {
		f.Target = "*"
	}
	if !validOperator(f.Operator) {
		return f, fmt.Errorf("unknown operator %q in %q", f.Operator, s)
	}
	if !validGenerator(f.Generator) {
		return f, fmt.Errorf("unknown generator %q in %q", f.Generator, s)
	}
	return f, nil
}

// ParseFilters convenience for a YAML list of filter strings.
func ParseFilters(in []string) ([]Filter, error) {
	out := make([]Filter, 0, len(in))
	for _, s := range in {
		f, err := ParseFilter(s)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// ResolveFilters walks every source in the SourceConfig and returns a
// map keyed by source URL holding the parsed Filter list each source
// should apply. The resolution rule mirrors source resolution: a
// child's `filter:` list REPLACES the default's; only when the child
// list is empty does it inherit the default. Webhook URLs are
// synthesised from `webhook://<path>` to match v1.Source.URL.
func (s *SourceConfig) ResolveFilters() (map[string][]Filter, error) {
	out := make(map[string][]Filter)

	resolve := func(common CommonFields, url string) error {
		strs := common.Filter
		if len(strs) == 0 {
			strs = s.Default.Filter
		}
		if len(strs) == 0 {
			return nil
		}
		fs, err := ParseFilters(strs)
		if err != nil {
			return fmt.Errorf("source %q: %w", common.Name, err)
		}
		out[url] = fs
		return nil
	}

	for i := range s.RSS {
		if err := resolve(s.RSS[i].CommonFields, s.RSS[i].URL); err != nil {
			return nil, err
		}
	}
	for i := range s.HTTP {
		if err := resolve(s.HTTP[i].CommonFields, s.HTTP[i].URL); err != nil {
			return nil, err
		}
	}
	for i := range s.Webhook {
		url := s.Webhook[i].URL
		if url == "" {
			url = "webhook://" + s.Webhook[i].Path
		}
		if err := resolve(s.Webhook[i].CommonFields, url); err != nil {
			return nil, err
		}
	}
	for i := range s.Websocket {
		if err := resolve(s.Websocket[i].CommonFields, s.Websocket[i].URL); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Eval true if the filter passes against doc. Wildcard target matches
// on first hit; negation flips the final result.
func (f Filter) Eval(d Doc) (bool, error) {
	targets := []string{f.Target}
	if f.Target == "*" {
		targets = d.Fields()
	}

	for _, t := range targets {
		v, ok := d.Field(t)
		if !ok {
			continue
		}
		match, err := f.evalOne(v)
		if err != nil {
			return false, err
		}
		if match {
			return !f.Negate, nil
		}
	}
	return f.Negate, nil // no match means false; negate flips
}

// evalOne generator produces a typed value, operator compares it.
func (f Filter) evalOne(v string) (bool, error) {
	switch f.Generator {
	case GenSelf:
		return cmpString(v, f.Operator, f.Value)
	case GenLen:
		return cmpInt(len(v), f.Operator, f.Value)
	case GenRegex:
		re, err := regexp.Compile(f.Value)
		if err != nil {
			return false, fmt.Errorf("regex %q: %w", f.Value, err)
		}
		return cmpBool(re.MatchString(v), f.Operator, "true")
	case GenContains:
		return cmpBool(strings.Contains(v, f.Value), f.Operator, "true")
	case GenEmpty:
		return cmpBool(v == "", f.Operator, "true")
	case GenSim, GenCount:
		return false, fmt.Errorf("generator %q not implemented", f.Generator)
	}
	return false, fmt.Errorf("unhandled generator %q", f.Generator)
}

func cmpString(a string, op Operator, b string) (bool, error) {
	switch op {
	case OpEq:
		return a == b, nil
	case OpNe:
		return a != b, nil
	case OpGt:
		return a > b, nil
	case OpGte:
		return a >= b, nil
	case OpLt:
		return a < b, nil
	case OpLte:
		return a <= b, nil
	}
	return false, fmt.Errorf("bad op %q for string", op)
}

func cmpInt(a int, op Operator, raw string) (bool, error) {
	b, err := strconv.Atoi(raw)
	if err != nil {
		return false, fmt.Errorf("int value %q: %w", raw, err)
	}
	switch op {
	case OpEq:
		return a == b, nil
	case OpNe:
		return a != b, nil
	case OpGt:
		return a > b, nil
	case OpGte:
		return a >= b, nil
	case OpLt:
		return a < b, nil
	case OpLte:
		return a <= b, nil
	}
	return false, fmt.Errorf("bad op %q for int", op)
}

func cmpBool(a bool, op Operator, raw string) (bool, error) {
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("bool value %q: %w", raw, err)
	}
	switch op {
	case OpEq:
		return a == b, nil
	case OpNe:
		return a != b, nil
	}
	return false, fmt.Errorf("bad op %q for bool", op)
}

func validOperator(op Operator) bool {
	switch op {
	case OpEq, OpNe, OpGt, OpGte, OpLt, OpLte:
		return true
	}
	return false
}

func validGenerator(g Generator) bool {
	switch g {
	case GenSelf, GenLen, GenSim, GenRegex, GenContains, GenCount, GenEmpty:
		return true
	}
	return false
}
