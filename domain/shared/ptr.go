package shared

// PtrCopy returns a pointer to a copy of *p, or nil if p is nil. Use it to
// defend pointer-typed fields against caller-side mutation in getters and
// intake paths without repeating the same nil-check / dereference / address-of
// idiom at every call site (issue #109).
//
// Example:
//
//	func (a *Aggregate) Field() *T { return shared.PtrCopy(a.field) }
func PtrCopy[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
