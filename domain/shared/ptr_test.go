package shared

import "testing"

func TestPtrCopy_Nil(t *testing.T) {
	if got := PtrCopy[string](nil); got != nil {
		t.Errorf("PtrCopy(nil) = %v, want nil", got)
	}
}

func TestPtrCopy_String_IsolatesPointee(t *testing.T) {
	src := "original"
	cp := PtrCopy(&src)
	if cp == nil || *cp != "original" {
		t.Fatalf("PtrCopy(&src) = %v, want pointer to \"original\"", cp)
	}
	if cp == &src {
		t.Fatal("PtrCopy must return a NEW pointer, not the input pointer")
	}
	*cp = "mutated-via-copy"
	if src != "original" {
		t.Errorf("mutating the copy leaked back to the source: src = %q", src)
	}
}

func TestPtrCopy_Struct_DoesShallowCopy(t *testing.T) {
	type box struct {
		v int
		s []int
	}
	src := &box{v: 1, s: []int{10, 20}}
	cp := PtrCopy(src)
	if cp == nil || cp == src {
		t.Fatalf("PtrCopy(&struct) must return a new pointer, got %v", cp)
	}
	cp.v = 99
	if src.v != 1 {
		t.Errorf("mutating cp.v leaked to src.v: %d", src.v)
	}
	// PtrCopy is a SHALLOW copy by design — slices/maps inside still alias.
	// Callers needing deep copy must combine PtrCopy with a domain-specific
	// clone() method (see contract/trial.go and contract/suspension.go).
	cp.s[0] = 999
	if src.s[0] != 999 {
		t.Errorf("PtrCopy is documented as shallow; expected slice aliasing but src.s[0] = %d", src.s[0])
	}
}
