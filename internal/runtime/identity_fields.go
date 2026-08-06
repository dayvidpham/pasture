package runtime

// DeclaredField returns the declared native identity field with the exact
// native name, if the pinned mapping declares one. It is the single lookup
// used by every lifecycle frontend and by the waist L2 transform, replacing the
// four private findDeclaredField copies that previously drifted per package.
//
// The match is by exact native spelling: correlation fields are never
// normalised, so an unpadded, case-sensitive equality is the only correct
// comparison. The returned NativeIdentityField is a value copy; callers cannot
// mutate the pinned mapping through it.
func (m LifecycleEventMapping) DeclaredField(nativeName string) (NativeIdentityField, bool) {
	for _, field := range m.identities {
		if field.NativeName() == nativeName {
			return field, true
		}
	}
	return NativeIdentityField{}, false
}
