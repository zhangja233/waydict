//go:build darwin

package platform

func Current() Services {
	return unavailableServices("darwin")
}
