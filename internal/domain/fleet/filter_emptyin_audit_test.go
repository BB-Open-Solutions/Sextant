package fleet

import "testing"

// TestValidateFilterRejectsEmptyInValue guards against a widening bug:
// matchesRule's OpIn branch does a plain equality check (got == v), so an
// empty string in Values matches every device whose attribute is unset -
// {attr:class, op:in, values:[laptop,""]} would silently select every
// classless device too. eq/ne/prefix already reject an empty Value; "in"
// must reject an empty entry the same way.
func TestValidateFilterRejectsEmptyInValue(t *testing.T) {
	fl := Filter{Rules: []FilterRule{
		{Attr: AttrClass, Op: OpIn, Values: []string{"laptop", ""}},
	}}
	if err := ValidateFilter(fl); err == nil {
		t.Fatal("empty entry in an \"in\" rule accepted")
	}
}

// TestValidateFilterAcceptsNonEmptyInValues is the control case.
func TestValidateFilterAcceptsNonEmptyInValues(t *testing.T) {
	fl := Filter{Rules: []FilterRule{
		{Attr: AttrClass, Op: OpIn, Values: []string{"laptop", "desktop"}},
	}}
	if err := ValidateFilter(fl); err != nil {
		t.Fatalf("valid \"in\" rule rejected: %v", err)
	}
}
