//go:build !linux && !darwin

package platform

func Current() Services {
	return unavailableServices("unsupported")
}
