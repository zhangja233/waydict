//go:build darwin && (!coreaudio || !cgo)

package platform

func Current() Services {
	return currentDarwinServices()
}
