package fleet

import "fmt"

// secretref.go: the secret-reference registry. Sextant never stores a secret
// value - config-as-data lands in a git-tracked file. Instead the org
// registers the NAMES of secrets that live in the real secret store (agenix on
// the device, a Secret in the cluster), and secret-typed settings reference a
// name. The generator resolves the name to a path on the device. This keeps
// every secret out of git and out of the console.

// SecretRef describes a named secret known to exist in the secret store. The
// map key is the name (the agenix/Secret key); Description is operator-facing.
type SecretRef struct {
	Description string `json:"description,omitempty"`
}

// AddSecretRef registers a secret name. The name is a slug: it becomes an
// agenix attribute and a settings value, so it must be path- and JSON-safe.
func AddSecretRef(name string, ref SecretRef) Mutation {
	return func(f *Fleet) error {
		if !ValidateSlug(name) {
			return fmt.Errorf("invalid secret name %q (lowercase slug required)", name)
		}
		if _, exists := f.SecretRefs[name]; exists {
			return fmt.Errorf("secret reference %q already exists", name)
		}
		if f.SecretRefs == nil {
			f.SecretRefs = map[string]SecretRef{}
		}
		f.SecretRefs[name] = ref
		return nil
	}
}

// RemoveSecretRef unregisters a secret name. Any setting still pointing at it
// keeps a dangling reference the generator will reject at build time, so the
// caller should surface that; the registry itself just drops the name.
func RemoveSecretRef(name string) Mutation {
	return func(f *Fleet) error {
		if _, ok := f.SecretRefs[name]; !ok {
			return fmt.Errorf("unknown secret reference %q", name)
		}
		delete(f.SecretRefs, name)
		return nil
	}
}

// HasSecretRef reports whether a secret name is registered - the settings
// write path checks this so a secret setting can only reference a known name.
func (f *Fleet) HasSecretRef(name string) bool {
	_, ok := f.SecretRefs[name]
	return ok
}
