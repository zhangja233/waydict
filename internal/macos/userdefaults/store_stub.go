//go:build !darwin || !cgo

package userdefaults

import "waydict/internal/preferences"

func New() preferences.Store {
	return preferences.NewMemoryStore()
}
